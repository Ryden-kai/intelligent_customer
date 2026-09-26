package seed

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"intelligent_customer/backend/internal/db"
)

// openTempDB 创建一个应用了 migration 的临时 SQLite。供 seed 测试复用。
func openTempDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	conn, err := db.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(conn, db.MigrationsFS, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, func() {}
}

// TestRunRBACSeed_FirstRun 验证首次执行后 5 张表 + 数据都在。
func TestRunRBACSeed_FirstRun(t *testing.T) {
	conn, _ := openTempDB(t)
	ctx := context.Background()

	res, err := RunRBACSeed(ctx, conn)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 首次启动：25 permissions + 3 roles + 5 rate_limit_configs 全部插入。
	if res.PermissionsInserted != 25 {
		t.Errorf("permissions inserted = %d, want 25", res.PermissionsInserted)
	}
	if res.RolesInserted != 3 {
		t.Errorf("roles inserted = %d, want 3", res.RolesInserted)
	}
	if res.RateLimitsInserted != 5 {
		t.Errorf("rate_limits inserted = %d, want 5", res.RateLimitsInserted)
	}

	// 二次校验：实际行数一致。
	for _, table := range []string{"permissions", "roles", "rate_limit_configs"} {
		var n int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		switch table {
		case "permissions":
			if n != 25 {
				t.Errorf("%s rows = %d, want 25", table, n)
			}
		case "roles":
			if n != 3 {
				t.Errorf("%s rows = %d, want 3", table, n)
			}
		case "rate_limit_configs":
			if n != 5 {
				t.Errorf("%s rows = %d, want 5", table, n)
			}
		}
	}
}

// TestRunRBACSeed_Idempotent 验证二次启动后行数不变。
func TestRunRBACSeed_Idempotent(t *testing.T) {
	conn, _ := openTempDB(t)
	ctx := context.Background()

	// 第一次。
	if _, err := RunRBACSeed(ctx, conn); err != nil {
		t.Fatalf("seed first: %v", err)
	}
	// 第二次：应该全部 OR IGNORE 返回 0。
	res, err := RunRBACSeed(ctx, conn)
	if err != nil {
		t.Fatalf("seed second: %v", err)
	}
	if res.PermissionsInserted != 0 {
		t.Errorf("permissions inserted on 2nd run = %d, want 0", res.PermissionsInserted)
	}
	if res.RolesInserted != 0 {
		t.Errorf("roles inserted on 2nd run = %d, want 0", res.RolesInserted)
	}
	if res.RateLimitsInserted != 0 {
		t.Errorf("rate_limits inserted on 2nd run = %d, want 0", res.RateLimitsInserted)
	}

	// 行数仍然是 25/3/5。
	var p, r, l int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM permissions`).Scan(&p)
	_ = conn.QueryRow(`SELECT COUNT(*) FROM roles`).Scan(&r)
	_ = conn.QueryRow(`SELECT COUNT(*) FROM rate_limit_configs`).Scan(&l)
	if p != 25 || r != 3 || l != 5 {
		t.Errorf("after 2nd run: permissions=%d roles=%d ratelimits=%d, want 25/3/5", p, r, l)
	}
}

// TestPermissionSeeds 验证预置数据完整性。
func TestPermissionSeeds(t *testing.T) {
	seeds := PermissionSeeds()
	if len(seeds) != 25 {
		t.Fatalf("permission seeds = %d, want 25", len(seeds))
	}
	// 验证关键权限码都在。
	need := map[string]bool{
		"jev.template.read":     true,
		"conversation.write":    true,
		"audit.read":            true,
		"role.manage":           true,
		"ratelimit.manage":      true,
		"chat.use":              true,
		"feedback.submit":       true,
	}
	for _, p := range seeds {
		delete(need, p.Code)
	}
	if len(need) != 0 {
		t.Errorf("missing permission codes: %v", need)
	}
}

// TestRoleSeeds 验证 3 个 system role 的权限字段非空。
func TestRoleSeeds(t *testing.T) {
	roles := RoleSeeds()
	if len(roles) != 3 {
		t.Fatalf("role seeds = %d, want 3", len(roles))
	}
	for _, r := range roles {
		if len(r.Permissions) == 0 {
			t.Errorf("role %s has empty permissions", r.Name)
		}
		if !r.IsSystem {
			t.Errorf("seed role %s should be system", r.Name)
		}
	}
	// admin 必须含 ['*']。
	hasAdminWildcard := false
	for _, r := range roles {
		if r.Name == "admin" {
			for _, p := range r.Permissions {
				if p == "*" {
					hasAdminWildcard = true
				}
			}
		}
	}
	if !hasAdminWildcard {
		t.Errorf("admin role missing '*' wildcard permission")
	}
}

// TestRateLimitSeeds 验证 5 个限流配置 + 关键维度。
func TestRateLimitSeeds(t *testing.T) {
	rls := RateLimitSeeds()
	if len(rls) != 5 {
		t.Fatalf("rate limit seeds = %d, want 5", len(rls))
	}
	// 验证关键端点都在。
	hasLoginIP := false
	for _, r := range rls {
		if r.Endpoint == "POST:/api/auth/login" && r.Dimension == "ip" && r.PerMinute == 10 {
			hasLoginIP = true
		}
	}
	if !hasLoginIP {
		t.Errorf("missing POST:/api/auth/login ip=10/min seed")
	}
}

// TestSanitizeID 验证 endpoint 字符串清洗。
func TestSanitizeID(t *testing.T) {
	cases := map[string]string{
		"POST:/api/auth/login":   "post-api-auth-login",
		"ALL:/api/admin":         "all-api-admin",
		"POST:/api/chat":         "post-api-chat",
		"GET:/api/admin/jev/foo": "get-api-admin-jev-foo",
	}
	for in, want := range cases {
		got := sanitizeID(in)
		if got != want {
			t.Errorf("sanitizeID(%q) = %q, want %q", in, got, want)
		}
	}
}