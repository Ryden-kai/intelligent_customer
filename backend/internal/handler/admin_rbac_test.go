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

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/db"
	dbseed "intelligent_customer/backend/internal/db/seed"
	"intelligent_customer/backend/internal/handler"
	"intelligent_customer/backend/internal/rbac"
)

// newRBACHarness 构造 RBAC handler 的测试环境：
//
//   - 应用 migration + RBAC seed
//   - 把 admin RBAC 端点挂到 chi.Router
//   - 返回 router + *handler.AdminRBAC
func newRBACHarness(t *testing.T) (http.Handler, *handler.AdminRBAC) {
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

	repo := rbac.NewRepo(conn)
	rbacH := &handler.AdminRBAC{Repo: repo, Logger: zerolog.Nop()}

	r := chi.NewRouter()
	r.Get("/api/admin/roles", rbacH.ListRoles)
	r.Get("/api/admin/roles/{id}", rbacH.GetRole)
	r.Post("/api/admin/roles", rbacH.CreateRole)
	r.Put("/api/admin/roles/{id}", rbacH.UpdateRole)
	r.Delete("/api/admin/roles/{id}", rbacH.DeleteRole)
	r.Get("/api/admin/permissions", rbacH.ListPermissions)
	r.Get("/api/admin/users/{user_id}/roles", rbacH.GetUserRoles)
	r.Put("/api/admin/users/{user_id}/roles", rbacH.SetUserRoles)
	return r, rbacH
}

// ----------------------------------------------------------------------------
//  7 端点集成测试
// ----------------------------------------------------------------------------

func TestRBAC_ListRoles_Returns3SystemRoles(t *testing.T) {
	r, _ := newRBACHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/roles", nil)
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
	if body.Total != 3 {
		t.Errorf("total=%d, want 3", body.Total)
	}
}

func TestRBAC_GetRole_OK(t *testing.T) {
	r, _ := newRBACHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/roles/role-admin-tnt_default", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["name"] != "admin" {
		t.Errorf("name=%v, want admin", body["name"])
	}
}

func TestRBAC_GetRole_NotFound(t *testing.T) {
	r, _ := newRBACHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/roles/role-bogus", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.Code)
	}
}

func TestRBAC_CreateRole_AndGet(t *testing.T) {
	r, _ := newRBACHarness(t)
	body, _ := json.Marshal(map[string]any{
		"name":        "viewer",
		"description": "Read-only viewer",
		"permissions": []string{"stats.read", "audit.read"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/roles", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	id, _ := resp["id"].(string)
	if !strings.HasPrefix(id, "role-") {
		t.Errorf("id=%v, want role- prefix", id)
	}

	// 再查应能取回。
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/roles/"+id, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("after create: status=%d", w2.Code)
	}
}

func TestRBAC_CreateRole_RejectsUnknownPerm(t *testing.T) {
	r, _ := newRBACHarness(t)
	body, _ := json.Marshal(map[string]any{
		"name":        "bad",
		"permissions": []string{"stats.read", "totally.bogus"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/roles", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.Code)
	}
}

func TestRBAC_CreateRole_RejectsEmptyPerms(t *testing.T) {
	r, _ := newRBACHarness(t)
	body, _ := json.Marshal(map[string]any{
		"name":        "bad",
		"permissions": []string{},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/roles", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.Code)
	}
}

func TestRBAC_UpdateRole_Happy(t *testing.T) {
	r, _ := newRBACHarness(t)
	body, _ := json.Marshal(map[string]any{
		"name":        "viewer2",
		"permissions": []string{"stats.read", "audit.read"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/roles", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status=%d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	id := resp["id"].(string)

	// 更新描述。
	upd, _ := json.Marshal(map[string]any{"description": "Updated desc"})
	req2 := httptest.NewRequest(http.MethodPut, "/api/admin/roles/"+id, bytes.NewReader(upd))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("update: status=%d body=%s", w2.Code, w2.Body.String())
	}
}

func TestRBAC_DeleteRole_Happy(t *testing.T) {
	r, _ := newRBACHarness(t)
	body, _ := json.Marshal(map[string]any{
		"name":        "tmp",
		"permissions": []string{"stats.read"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/roles", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	id := resp["id"].(string)

	req2 := httptest.NewRequest(http.MethodDelete, "/api/admin/roles/"+id, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNoContent {
		t.Errorf("delete: status=%d", w2.Code)
	}
}

func TestRBAC_DeleteRole_RejectsSystem(t *testing.T) {
	r, _ := newRBACHarness(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/admin/roles/role-admin-tnt_default", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status=%d, want 403", w.Code)
	}
}

func TestRBAC_ListPermissions_Returns25(t *testing.T) {
	r, _ := newRBACHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/permissions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var body struct {
		Permissions []map[string]any `json:"permissions"`
		Groups      map[string][]string `json:"groups"`
		Total       int              `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Total != 25 {
		t.Errorf("total=%d, want 25", body.Total)
	}
	if body.Groups == nil || len(body.Groups) == 0 {
		t.Errorf("groups missing")
	}
}

func TestRBAC_GetUserRoles_Empty(t *testing.T) {
	r, _ := newRBACHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/users/u-1/roles", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["total"].(float64) != 0 {
		t.Errorf("total=%v, want 0", body["total"])
	}
}

func TestRBAC_SetUserRoles_AndGet(t *testing.T) {
	r, _ := newRBACHarness(t)
	body, _ := json.Marshal(map[string]any{
		"role_ids": []string{"role-agent-tnt_default"},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/admin/users/u-1/roles", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set: status=%d body=%s", w.Code, w.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/users/u-1/roles", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("get: status=%d", w2.Code)
	}
	var resp struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &resp)
	if resp.Total != 1 {
		t.Errorf("total=%d, want 1", resp.Total)
	}
}

func TestRBAC_CreateRole_BadJSON(t *testing.T) {
	r, _ := newRBACHarness(t)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/roles", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.Code)
	}
}

func TestRBAC_CreateRole_MissingName(t *testing.T) {
	r, _ := newRBACHarness(t)
	body, _ := json.Marshal(map[string]any{
		"permissions": []string{"stats.read"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/roles", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.Code)
	}
}