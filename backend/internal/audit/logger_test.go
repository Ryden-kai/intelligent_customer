package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/db"
	dbseed "intelligent_customer/backend/internal/db/seed"
)

// openTempDB 应用 migration + RBAC seed（audit_logs 表在 migration 007 中）。
func openTempDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	conn, err := db.Open(path)
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

// newLogger 构造 Logger：channel 64 / flush 50ms / fallback 临时文件。
func newLogger(t *testing.T, repo *Repo) *Logger {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "audit_failed.jsonl")
	l, err := NewLogger(repo, zerolog.Nop(),
		WithChannelBuffer(64),
		WithFlushInterval(50*time.Millisecond),
		WithFailedPath(tmp),
	)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	return l
}

// ctxWithClaims 把 *auth.Claims 注入 ctx。
func ctxWithClaims(ctx context.Context, c *auth.Claims) context.Context {
	return auth.WithClaims(ctx, c)
}

// ----------------------------------------------------------------------------
// 10+ 测试用例
// ----------------------------------------------------------------------------

// TestLogger_EmitAndFlush 验证单条 Emit 在 flush 后落库。
func TestLogger_EmitAndFlush(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	l := newLogger(t, repo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)

	c := &auth.Claims{Username: "alice", Email: "alice@demo"}
	emitCtx := ctxWithClaims(context.Background(), c)
	l.Emit(emitCtx, ActionAuthLogin, "user", "u-1", nil)

	// 等 flush。
	time.Sleep(200 * time.Millisecond)

	// 关闭。
	_ = l.Stop(time.Second)

	logs, total, err := repo.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 {
		t.Fatalf("total=%d, want 1", total)
	}
	if logs[0].Action != ActionAuthLogin {
		t.Errorf("action=%s, want %s", logs[0].Action, ActionAuthLogin)
	}
	if logs[0].ActorID != "alice" {
		t.Errorf("actor_id=%s, want alice", logs[0].ActorID)
	}
}

// TestLogger_EmitFromRequest_CapturesIPAndUA 验证 EmitFromRequest 抽取 IP/UA。
func TestLogger_EmitFromRequest_CapturesIPAndUA(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	l := newLogger(t, repo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)

	c := &auth.Claims{Username: "bob"}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/x", strings.NewReader("{}"))
	req.Header.Set("User-Agent", "test-ua/1.0")
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req = req.WithContext(ctxWithClaims(req.Context(), c))
	l.EmitFromRequest(req, ActionAuthLogin, "user", "u-1", nil)
	time.Sleep(200 * time.Millisecond)
	_ = l.Stop(time.Second)

	logs, _, _ := repo.List(context.Background(), Filter{})
	if len(logs) != 1 {
		t.Fatalf("total=%d, want 1", len(logs))
	}
	if logs[0].IP != "1.2.3.4" {
		t.Errorf("ip=%s, want 1.2.3.4", logs[0].IP)
	}
	if logs[0].UserAgent != "test-ua/1.0" {
		t.Errorf("ua=%s, want test-ua/1.0", logs[0].UserAgent)
	}
}

// TestLogger_ChannelFull_DropsAndCounts 验证 channel 满时丢消息但计数增长。
func TestLogger_ChannelFull_DropsAndCounts(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	// 用 2 大小的 channel 让它快速满。
	l, err := NewLogger(repo, zerolog.Nop(),
		WithChannelBuffer(2),
		WithFlushInterval(10*time.Hour), // 长间隔避免自动 flush
		WithFailedPath(filepath.Join(t.TempDir(), "f.jsonl")),
	)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	// 不启动 Run，让 channel 持续积压。
	c := &auth.Claims{Username: "alice"}
	for i := 0; i < 10; i++ {
		l.Emit(ctxWithClaims(context.Background(), c), ActionAuthLogin, "user", "u", nil)
	}
	dropped, _ := l.Stats()
	if dropped == 0 {
		t.Errorf("dropped=%d, want >0", dropped)
	}
}

// TestLogger_FlushRecovery 验证单批 insert 成功后 dropped/failed 计数为 0。
func TestLogger_FlushRecovery(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	l := newLogger(t, repo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)

	c := &auth.Claims{Username: "alice"}
	for i := 0; i < 5; i++ {
		l.Emit(ctxWithClaims(context.Background(), c), ActionSkillCreate, "skill", "s-1", nil)
	}
	time.Sleep(200 * time.Millisecond)
	_ = l.Stop(time.Second)

	dropped, failed := l.Stats()
	if dropped != 0 || failed != 0 {
		t.Errorf("stats dropped=%d failed=%d, want 0/0", dropped, failed)
	}
	_, total, _ := repo.List(context.Background(), Filter{})
	if total != 5 {
		t.Errorf("total=%d, want 5", total)
	}
}

// TestLogger_StopTimeout 验证 Stop 在无 flush 时也能快速返回。
func TestLogger_StopTimeout(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	l := newLogger(t, repo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)

	// 不发消息；Stop 应立即 flush 空批并返回。
	if err := l.Stop(time.Second); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// TestLogger_NewLogger_NilRepo 验证 nil repo 直接报错。
func TestLogger_NewLogger_NilRepo(t *testing.T) {
	_, err := NewLogger(nil, zerolog.Nop())
	if err == nil {
		t.Errorf("expected error for nil repo")
	}
}

// TestLogger_PayloadAsJSON 验证 payload 在落库时被序列化为 JSON。
func TestLogger_PayloadAsJSON(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	l := newLogger(t, repo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)

	c := &auth.Claims{Username: "alice"}
	payload := Payload{"template_id": "t-1", "old_status": "draft", "new_status": "published"}
	l.Emit(ctxWithClaims(context.Background(), c), ActionTemplatePublish, "template", "t-1", payload)
	time.Sleep(200 * time.Millisecond)
	_ = l.Stop(time.Second)

	logs, _, _ := repo.List(context.Background(), Filter{})
	if len(logs) != 1 {
		t.Fatalf("total=%d, want 1", len(logs))
	}
	if logs[0].Payload["template_id"] != "t-1" {
		t.Errorf("payload=%v, want template_id=t-1", logs[0].Payload)
	}
}

// TestLogger_EmitEmptyAction 验证空 action 直接丢弃。
func TestLogger_EmitEmptyAction(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	l := newLogger(t, repo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)

	c := &auth.Claims{Username: "alice"}
	l.Emit(ctxWithClaims(context.Background(), c), "", "user", "u-1", nil)
	time.Sleep(200 * time.Millisecond)
	_ = l.Stop(time.Second)

	_, total, _ := repo.List(context.Background(), Filter{})
	if total != 0 {
		t.Errorf("total=%d, want 0 (empty action should be dropped)", total)
	}
}

// TestLogger_FallbackJSONL_OnDBError 模拟 DB 不可用后事件落兜底文件。
//
// 通过 Repo 包装一个故意坏的连接（指向不存在的 SQLite 文件），保证 InsertBatch
// 必失败，从而触发 fallback 写 jsonl。
func TestLogger_FallbackJSONL_OnDBError(t *testing.T) {
	// 构造一个连不上 DB 的 Repo：直接用不存在的文件路径（不用 _modernc.org/sqlite Open）。
	badConn, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "nonexistent.db"))
	if err != nil {
		t.Fatalf("open bad: %v", err)
	}
	t.Cleanup(func() { _ = badConn.Close() })

	repo := &Repo{DB: badConn}

	tmp := filepath.Join(t.TempDir(), "audit_failed.jsonl")
	l, err := NewLogger(repo, zerolog.Nop(),
		WithChannelBuffer(8),
		WithFlushInterval(20*time.Millisecond),
		WithFailedPath(tmp),
	)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)

	c := &auth.Claims{Username: "alice"}
	l.Emit(ctxWithClaims(context.Background(), c), ActionAuthLogin, "user", "u-1", nil)
	l.Emit(ctxWithClaims(context.Background(), c), ActionAuthLogin, "user", "u-2", nil)

	// 等待 flush + 3 次重试（100ms+500ms+2s = 2.6s）+ jsonl 写入。
	time.Sleep(4 * time.Second)
	_ = l.Stop(time.Second)

	if _, err := os.Stat(tmp); os.IsNotExist(err) {
		t.Errorf("fallback file %s missing", tmp)
	} else {
		b, err := os.ReadFile(tmp)
		if err != nil {
			t.Fatalf("read fallback: %v", err)
		}
		if len(b) == 0 {
			t.Errorf("fallback file empty")
		}
	}
}

// TestLogger_Concurrent_Emit 验证并发 Emit 不丢计数。
func TestLogger_Concurrent_Emit(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	l := newLogger(t, repo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)

	c := &auth.Claims{Username: "alice"}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Emit(ctxWithClaims(context.Background(), c), ActionAuthLogin, "user", "u", nil)
		}()
	}
	wg.Wait()
	time.Sleep(300 * time.Millisecond)
	_ = l.Stop(time.Second)

	_, total, _ := repo.List(context.Background(), Filter{})
	if total != 20 {
		t.Errorf("total=%d, want 20", total)
	}
}

// TestLogger_NoClaims_StillInserts 验证无 Claims 时 actor 为空（不报错）。
func TestLogger_NoClaims_StillInserts(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	l := newLogger(t, repo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Run(ctx)

	l.Emit(context.Background(), ActionAuthLogin, "user", "u-1", nil)
	time.Sleep(200 * time.Millisecond)
	_ = l.Stop(time.Second)

	logs, _, _ := repo.List(context.Background(), Filter{})
	if len(logs) != 1 {
		t.Fatalf("total=%d, want 1", len(logs))
	}
	if logs[0].ActorID != "" {
		t.Errorf("actor_id=%s, want empty", logs[0].ActorID)
	}
}

// ----------------------------------------------------------------------------
// Repo tests (separate small section)
// ----------------------------------------------------------------------------

// TestRepo_DistinctActors
func TestRepo_DistinctActors(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)

	// 直接 insert 几条。
	events := []Event{
		{ID: "log-1", Timestamp: time.Now(), ActorID: "alice", Action: ActionAuthLogin},
		{ID: "log-2", Timestamp: time.Now(), ActorID: "bob", Action: ActionAuthLogin},
		{ID: "log-3", Timestamp: time.Now(), ActorID: "alice", Action: ActionSkillCreate},
	}
	if err := repo.InsertBatch(context.Background(), events); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	actors, err := repo.DistinctActors(context.Background(), "")
	if err != nil {
		t.Fatalf("DistinctActors: %v", err)
	}
	if len(actors) != 2 {
		t.Errorf("actors=%v, want 2 unique", actors)
	}
}

// TestRepo_CountByAction 验证按 action 聚合。
func TestRepo_CountByAction(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)

	events := []Event{
		{ID: "log-1", Timestamp: time.Now(), ActorID: "alice", Action: ActionAuthLogin},
		{ID: "log-2", Timestamp: time.Now(), ActorID: "bob", Action: ActionAuthLogin},
		{ID: "log-3", Timestamp: time.Now(), ActorID: "alice", Action: ActionSkillCreate},
	}
	if err := repo.InsertBatch(context.Background(), events); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	counts, err := repo.CountByAction(context.Background(), "", time.Time{})
	if err != nil {
		t.Fatalf("CountByAction: %v", err)
	}
	loginCount := 0
	for _, c := range counts {
		if c.Action == ActionAuthLogin {
			loginCount = c.Count
		}
	}
	if loginCount != 2 {
		t.Errorf("login count=%d, want 2", loginCount)
	}
}

// TestRepo_ExportCSV_BOMAndContent 验证 CSV 含 BOM + 内容。
func TestRepo_ExportCSV_BOMAndContent(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)

	events := []Event{
		{ID: "log-csv-1", Timestamp: time.Now(), ActorID: "alice", ActorEmail: "a@b",
			Action: ActionAuthLogin, TargetType: "user", TargetID: "u-1",
			IP: "1.2.3.4", UserAgent: "ua", Payload: Payload{"foo": "bar"}},
	}
	if err := repo.InsertBatch(context.Background(), events); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	buf, err := repo.ExportCSV(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("ExportCSV: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "\xEF\xBB\xBF") {
		t.Errorf("missing UTF-8 BOM")
	}
	body := buf.String()
	if !strings.Contains(body, "auth.login") {
		t.Errorf("missing action header")
	}
	if !strings.Contains(body, "alice") {
		t.Errorf("missing actor_id")
	}
}

// keep imports referenced
var _ = json.Marshal