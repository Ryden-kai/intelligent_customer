package rbac_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/rbac"
)

// withClaims 把 *auth.Claims 塞入 ctx 的小工具。
func withClaims(ctx context.Context, c *auth.Claims) context.Context {
	return auth.WithClaims(ctx, c)
}

// TestRequirePermission_Pass 验证有权限码的请求通过中间件。
func TestRequirePermission_Pass(t *testing.T) {
	h := rbac.RequirePermission("stats.read")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)
	c := &auth.Claims{Username: "alice", Role: "agent", Permissions: []string{"stats.read"}}
	req := httptest.NewRequest("GET", "/api/admin/x", nil)
	req = req.WithContext(withClaims(req.Context(), c))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// TestRequirePermission_Forbidden 验证无权限码时返回 403。
func TestRequirePermission_Forbidden(t *testing.T) {
	h := rbac.RequirePermission("stats.export")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("downstream should NOT be called")
		}),
	)
	c := &auth.Claims{Username: "alice", Role: "agent", Permissions: []string{"stats.read"}}
	req := httptest.NewRequest("GET", "/api/admin/x", nil)
	req = req.WithContext(withClaims(req.Context(), c))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"code":"forbidden"`) {
		t.Errorf("body = %q, want forbidden code", body)
	}
}

// TestRequirePermission_Wildcard 验证 admin 通配符 "*" 放行所有 code。
func TestRequirePermission_Wildcard(t *testing.T) {
	h := rbac.RequirePermission("any.code.you.want")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)
	c := &auth.Claims{Username: "admin", Role: "admin", Permissions: []string{"*"}}
	req := httptest.NewRequest("GET", "/api/admin/x", nil)
	req = req.WithContext(withClaims(req.Context(), c))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// TestRequirePermission_NoClaims 验证 ctx 没 Claims 时返回 403（auth_required）。
func TestRequirePermission_NoClaims(t *testing.T) {
	h := rbac.RequirePermission("stats.read")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("downstream should NOT be called")
		}),
	)
	req := httptest.NewRequest("GET", "/api/admin/x", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), "auth_required") {
		t.Errorf("body = %q, want auth_required", w.Body.String())
	}
}

// TestRequirePermission_NilClaims 验证 ctx 里的 *Claims 为 nil 时也走 forbidden。
func TestRequirePermission_NilClaims(t *testing.T) {
	// 通过 context.WithValue 把 nil 塞进 ctx；这里直接构造一个没有 Claims
	// 的 request 来模拟 ctx 里完全没有 *Claims。
	req := httptest.NewRequest("GET", "/api/admin/x", nil)
	// 显式把 *Claims 塞成 nil，调用 PermissionsFromCtx 会得到 (nil, true)。
	// 但中间件只看 ok，不看 c 是不是 nil——只要 ok=true 就尝试 HasPermission(nil)→false → 403。
	// 实际上 PermissionsFromCtx 在 c==nil 时返回 (nil, false)，
	// 所以中间件进入 403 auth_required 路径。
	req = req.WithContext(auth.WithClaims(req.Context(), nil))
	h := rbac.RequirePermission("stats.read")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("downstream should NOT be called")
		}),
	)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

// TestPermissionsFromCtx_PassThrough 验证 PermissionsFromCtx 读 ctx。
func TestPermissionsFromCtx_PassThrough(t *testing.T) {
	c := &auth.Claims{Permissions: []string{"a", "b"}}
	req := httptest.NewRequest("GET", "/api/admin/x", nil)
	req = req.WithContext(auth.WithClaims(req.Context(), c))
	perms, ok := rbac.PermissionsFromCtx(req)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if len(perms) != 2 || perms[0] != "a" || perms[1] != "b" {
		t.Errorf("perms = %v", perms)
	}
}

// TestPermissionsFromCtx_NoClaims 验证无 Claims 时 ok=false。
func TestPermissionsFromCtx_NoClaims(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/admin/x", nil)
	_, ok := rbac.PermissionsFromCtx(req)
	if ok {
		t.Errorf("expected ok=false")
	}
}