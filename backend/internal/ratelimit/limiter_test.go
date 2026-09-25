package ratelimit_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"intelligent_customer/backend/internal/db"
	dbseed "intelligent_customer/backend/internal/db/seed"
	"intelligent_customer/backend/internal/ratelimit"
)

// openTempDB 应用 migration + RBAC seed 后返回 *sql.DB。
func openTempDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(conn, db.MigrationsFS, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := dbseed.RunRBACSeed(context.Background(), conn); err != nil {
		t.Fatalf("rbac seed: %v", err)
	}
	return conn
}

// ----------------------------------------------------------------------------
// Repo tests（4 用例）
// ----------------------------------------------------------------------------

func TestRepo_ListEnabled_Returns5FromSeeds(t *testing.T) {
	conn := openTempDB(t)
	repo := ratelimit.NewRepo(conn)
	configs, err := repo.ListEnabled(context.Background())
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(configs) != 5 {
		t.Errorf("got %d configs, want 5", len(configs))
	}
	// 必须包含登录 ip 10/min。
	foundLogin := false
	for _, c := range configs {
		if c.Endpoint == "POST:/api/auth/login" && c.Dimension == "ip" && c.PerMinute == 10 {
			foundLogin = true
		}
	}
	if !foundLogin {
		t.Errorf("login ip=10/min config missing")
	}
}

func TestRepo_Update_AndReload(t *testing.T) {
	conn := openTempDB(t)
	repo := ratelimit.NewRepo(conn)
	all, err := repo.ListAll(context.Background(), "")
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) == 0 {
		t.Fatalf("no configs")
	}
	id := all[0].ID
	originalPM := all[0].PerMinute
	originalBurst := all[0].Burst

	updated, err := repo.Update(context.Background(), id, ratelimit.Config{
		PerMinute: 99,
		Burst:     7,
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.PerMinute != 99 {
		t.Errorf("per_minute=%d, want 99", updated.PerMinute)
	}
	if updated.Burst != 7 {
		t.Errorf("burst=%d, want 7", updated.Burst)
	}

	// 还原。
	_, _ = repo.Update(context.Background(), id, ratelimit.Config{
		PerMinute: originalPM,
		Burst:     originalBurst,
		Enabled:   all[0].Enabled,
	})
}

func TestRepo_Update_NotFound(t *testing.T) {
	conn := openTempDB(t)
	repo := ratelimit.NewRepo(conn)
	_, err := repo.Update(context.Background(), "rlc-bogus", ratelimit.Config{PerMinute: 1})
	if err != ratelimit.ErrNotFound {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestRepo_GetByID(t *testing.T) {
	conn := openTempDB(t)
	repo := ratelimit.NewRepo(conn)
	all, _ := repo.ListAll(context.Background(), "")
	if len(all) == 0 {
		t.Fatalf("no configs")
	}
	got, err := repo.GetByID(context.Background(), all[0].ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != all[0].ID {
		t.Errorf("id mismatch")
	}
}

// ----------------------------------------------------------------------------
// LimiterMap tests（6 用例）
// ----------------------------------------------------------------------------

func newMap(t *testing.T) (*ratelimit.LimiterMap, *sql.DB) {
	t.Helper()
	conn := openTempDB(t)
	repo := ratelimit.NewRepo(conn)
	m, err := ratelimit.NewLimiterMap(context.Background(), repo, ratelimit.Config{})
	if err != nil {
		t.Fatalf("NewLimiterMap: %v", err)
	}
	return m, conn
}

func TestLimiterMap_Allow_NoConfig_AllowsByDefault(t *testing.T) {
	m, _ := newMap(t)
	// 用未配置的 endpoint → 没匹配 config → 放行（因为默认 disabled）。
	res := m.Allow(ratelimit.DimensionIP, "1.2.3.4", "POST:/api/unknown")
	if !res.OK {
		t.Errorf("unknown endpoint should allow (default disabled): %+v", res)
	}
}

func TestLimiterMap_Allow_EmptyDimension_Allows(t *testing.T) {
	m, _ := newMap(t)
	res := m.Allow(ratelimit.DimensionIP, "", "POST:/api/auth/login")
	if !res.OK {
		t.Errorf("empty dim value should allow")
	}
}

func TestLimiterMap_Allow_BurstThenBlock(t *testing.T) {
	// 直接构造 LimiterMap（用最小配置：1 token/min，burst=1）。
	// 第一次 Allow 通过，第二次触发限流。
	conn := openTempDB(t)
	repo := ratelimit.NewRepo(conn)
	// 通过 SQL 直接插入一条小阈值配置，绕过 seed。
	_, err := conn.Exec(
		`INSERT INTO rate_limit_configs(id, tenant_id, endpoint, dimension, per_minute, per_hour, burst, enabled, description, created_at, updated_at)
		 VALUES('rlc-test-tight', 'tnt_default', 'POST:/api/test/tight', 'ip', 1, 60, 1, 1, '', 0, 0)`)
	if err != nil {
		t.Fatalf("seed custom: %v", err)
	}
	m, err := ratelimit.NewLimiterMap(context.Background(), repo, ratelimit.Config{})
	if err != nil {
		t.Fatalf("NewLimiterMap: %v", err)
	}

	// 第一次允许。
	res1 := m.Allow(ratelimit.DimensionIP, "9.9.9.9", "POST:/api/test/tight")
	if !res1.OK {
		t.Errorf("first call should pass, got %+v", res1)
	}
	// 紧接第二次必然触限（burst=1）。
	res2 := m.Allow(ratelimit.DimensionIP, "9.9.9.9", "POST:/api/test/tight")
	if res2.OK {
		t.Errorf("second call should be blocked (burst=1), got %+v", res2)
	}
	if res2.RetryAfter <= 0 {
		t.Errorf("retry_after should be > 0, got %v", res2.RetryAfter)
	}
}

func TestLimiterMap_Allow_DifferentKeys_Independent(t *testing.T) {
	m, _ := newMap(t)
	// 不同 ip + login endpoint：seed 默认 10/min burst=5。
	// 两个 ip 各能拿到 5 个 token；不应相互影响。
	for i := 0; i < 10; i++ {
		r1 := m.Allow(ratelimit.DimensionIP, "10.0.0.1", "POST:/api/auth/login")
		r2 := m.Allow(ratelimit.DimensionIP, "10.0.0.2", "POST:/api/auth/login")
		if i < 5 {
			if !r1.OK || !r2.OK {
				t.Errorf("first 5 rounds: r1=%v r2=%v", r1, r2)
			}
		} else {
			// 第 6 次开始：每个 ip 的 burst 都耗尽 → 至少一个应该被 block。
			if r1.OK && r2.OK {
				t.Errorf("after burst: both should be blocked, got r1=%v r2=%v", r1, r2)
			}
		}
	}
}

func TestLimiterMap_Reload_PicksUpChanges(t *testing.T) {
	conn := openTempDB(t)
	repo := ratelimit.NewRepo(conn)
	m, err := ratelimit.NewLimiterMap(context.Background(), repo, ratelimit.Config{})
	if err != nil {
		t.Fatalf("NewLimiterMap: %v", err)
	}

	// 直接调 DB 改 endpoint 阈值，然后 Reload；再 Allow 应反映新值。
	if _, err := conn.Exec(
		`UPDATE rate_limit_configs SET per_minute=999, burst=999, updated_at=? WHERE endpoint='POST:/api/auth/login' AND dimension='ip'`,
		time.Now().UnixMilli()); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// 现在 login 限制宽到 999/min burst=999；10 次都应过。
	for i := 0; i < 10; i++ {
		res := m.Allow(ratelimit.DimensionIP, "8.8.8.8", "POST:/api/auth/login")
		if !res.OK {
			t.Errorf("after reload, call %d blocked: %+v", i, res)
		}
	}
}

func TestLimiterMap_RequestReload_Async(t *testing.T) {
	m, _ := newMap(t)
	// 多次 RequestReload 不应阻塞 / panic。
	for i := 0; i < 100; i++ {
		m.RequestReload()
	}
}

func TestBuildKey(t *testing.T) {
	got := ratelimit.BuildKey(ratelimit.DimensionIP, "1.2.3.4", "POST:/api/auth/login")
	want := "ip:1.2.3.4:POST:/api/auth/login"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// keep rate import referenced (used by NewLimiter in impl)
var _ = rate.Limit(0)