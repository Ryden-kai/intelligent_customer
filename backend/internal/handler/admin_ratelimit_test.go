package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/db"
	dbseed "intelligent_customer/backend/internal/db/seed"
	"intelligent_customer/backend/internal/handler"
	"intelligent_customer/backend/internal/ratelimit"
)

func newRateLimitHarness(t *testing.T) (http.Handler, *ratelimit.Repo) {
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
	repo := ratelimit.NewRepo(conn)
	h := &handler.AdminRateLimit{
		Repo:   repo,
		Logger: zerolog.Nop(),
		ReloadSink: func() {},
	}
	r := chi.NewRouter()
	r.Get("/api/admin/ratelimit/configs", h.ListConfigs)
	r.Put("/api/admin/ratelimit/configs/{id}", h.UpdateConfig)
	return r, repo
}

func TestRateLimit_List_Returns5Seeds(t *testing.T) {
	r, _ := newRateLimitHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/ratelimit/configs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var body struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Total != 5 {
		t.Errorf("total=%d, want 5", body.Total)
	}
}

func TestRateLimit_Update_Happy(t *testing.T) {
	r, repo := newRateLimitHarness(t)
	all, _ := repo.ListAll(context.Background(), "")
	if len(all) == 0 {
		t.Fatalf("no configs")
	}
	id := all[0].ID

	body, _ := json.Marshal(map[string]any{
		"per_minute": 25,
		"burst":      5,
		"enabled":    true,
		"description": "test updated",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/admin/ratelimit/configs/"+id, bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var cfg map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &cfg)
	if cfg["per_minute"].(float64) != 25 {
		t.Errorf("per_minute=%v, want 25", cfg["per_minute"])
	}
}

func TestRateLimit_Update_NotFound(t *testing.T) {
	r, _ := newRateLimitHarness(t)
	body, _ := json.Marshal(map[string]any{"per_minute": 10, "enabled": true})
	req := httptest.NewRequest(http.MethodPut, "/api/admin/ratelimit/configs/rlc-bogus", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.Code)
	}
}

func TestRateLimit_Update_BadJSON(t *testing.T) {
	r, _ := newRateLimitHarness(t)
	req := httptest.NewRequest(http.MethodPut, "/api/admin/ratelimit/configs/x", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.Code)
	}
}