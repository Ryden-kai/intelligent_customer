package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/audit"
	"intelligent_customer/backend/internal/db"
	dbseed "intelligent_customer/backend/internal/db/seed"
	"intelligent_customer/backend/internal/handler"
)

func newAuditHarness(t *testing.T) (http.Handler, *audit.Repo) {
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
		t.Fatalf("seed: %v", err)
	}
	repo := audit.NewRepo(conn)
	h := &handler.AdminAudit{Repo: repo, Logger: zerolog.Nop()}
	r := chi.NewRouter()
	r.Get("/api/admin/audit", h.List)
	r.Get("/api/admin/audit/export", h.Export)
	r.Get("/api/admin/audit/{id}", h.Get)
	return r, repo
}

func TestAudit_List_Happy(t *testing.T) {
	r, repo := newAuditHarness(t)

	// seed
	if err := repo.InsertBatch(context.Background(), []audit.Event{
		{ID: "log-a-1", Timestamp: time.Now(), ActorID: "alice", Action: audit.ActionAuthLogin},
		{ID: "log-a-2", Timestamp: time.Now(), ActorID: "bob", Action: audit.ActionSkillCreate},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/audit", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Total != 2 {
		t.Errorf("total=%d, want 2", body.Total)
	}
}

func TestAudit_List_Filter(t *testing.T) {
	r, repo := newAuditHarness(t)

	if err := repo.InsertBatch(context.Background(), []audit.Event{
		{ID: "log-f-1", Timestamp: time.Now(), ActorID: "alice", Action: audit.ActionAuthLogin},
		{ID: "log-f-2", Timestamp: time.Now(), ActorID: "bob", Action: audit.ActionSkillCreate},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/audit?action="+audit.ActionAuthLogin, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var body struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Total != 1 {
		t.Errorf("total=%d, want 1", body.Total)
	}
}

func TestAudit_Get_Happy(t *testing.T) {
	r, repo := newAuditHarness(t)

	if err := repo.InsertBatch(context.Background(), []audit.Event{
		{ID: "log-g-1", Timestamp: time.Now(), ActorID: "alice", Action: audit.ActionAuthLogin},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/audit/log-g-1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestAudit_Get_NotFound(t *testing.T) {
	r, _ := newAuditHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/audit/log-bogus", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.Code)
	}
}

func TestAudit_ExportCSV_BOMAndContent(t *testing.T) {
	r, repo := newAuditHarness(t)

	if err := repo.InsertBatch(context.Background(), []audit.Event{
		{ID: "log-csv-1", Timestamp: time.Now(), ActorID: "alice", Action: audit.ActionAuthLogin},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/audit/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	body := w.Body.Bytes()
	if len(body) < 3 || body[0] != 0xEF || body[1] != 0xBB || body[2] != 0xBF {
		t.Errorf("missing UTF-8 BOM")
	}
	if !bytes.Contains(body, []byte("auth.login")) {
		t.Errorf("CSV body missing action: %s", body)
	}
}

func TestAudit_List_TimeRangeFilter(t *testing.T) {
	r, repo := newAuditHarness(t)
	old := time.Now().Add(-48 * time.Hour)
	fresh := time.Now()
	if err := repo.InsertBatch(context.Background(), []audit.Event{
		{ID: "log-t-1", Timestamp: old, ActorID: "x", Action: audit.ActionAuthLogin},
		{ID: "log-t-2", Timestamp: fresh, ActorID: "y", Action: audit.ActionSkillCreate},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// from = 1h ago → 只应返回 log-t-2
	fromMs := time.Now().Add(-time.Hour).UnixMilli()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/audit?from="+strconvInt(fromMs), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var body struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Total != 1 {
		t.Errorf("total=%d, want 1", body.Total)
	}
}

func TestAudit_List_Pagination(t *testing.T) {
	r, repo := newAuditHarness(t)
	now := time.Now()
	evts := make([]audit.Event, 0, 10)
	for i := 0; i < 10; i++ {
		evts = append(evts, audit.Event{
			ID: "log-p-" + intStr(i), Timestamp: now.Add(time.Duration(i) * time.Second),
			ActorID: "x", Action: audit.ActionAuthLogin,
		})
	}
	if err := repo.InsertBatch(context.Background(), evts); err != nil {
		t.Fatalf("seed: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/audit?limit=3&offset=0", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var body struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Total != 10 {
		t.Errorf("total=%d, want 10", body.Total)
	}
}

// helpers
func strconvInt(v int64) string {
	return strings.TrimSpace(intToString(v))
}
func intToString(v int64) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
func intStr(i int) string {
	return intToString(int64(i))
}