package rbac

import (
	"net/http"

	"intelligent_customer/backend/internal/auth"
)

// PermissionsFromCtx 从 ctx 取出当前请求的权限码列表。
//
// 返回 (perms, ok)：当 RequireAuth 中间件没在 ctx 里塞入 Claims 时 ok=false。
// PR2 起 RequirePermission 中间件依赖该函数；测试中可单独 mock。
func PermissionsFromCtx(r *http.Request) ([]string, bool) {
	c, ok := auth.FromContext(r.Context())
	if !ok || c == nil {
		return nil, false
	}
	return c.Permissions, true
}

// RequirePermission 是 RBAC 网关：ctx 中无对应权限码时返回 403。
//
// 实现要点：
//
//  1. 中间件假定上游已挂 RequireAuth：ctx 里存在 *auth.Claims。
//     若 ctx 无 Claims（中间件顺序错或测试场景），返回 401（"auth_required"）。
//
//  2. 权限判定走 HasPermission(perms, code)；通配符 "*" 自动放行。
//
//  3. 返回 403 时附带 code/message + RequestID（由 writeAppError 处理）。
//
//  4. 中间件只对 /api/admin/* 子树生效（server.go 装配到 admin 子路由），
//     不影响 /api/chat、/api/feedback 等公开端点。
//
//  5. 注意：本中间件仅是"粗粒度拦截"——业务 handler 仍需对资源 ID 做细粒度
//     校验（如只能改自己创建的模板）。v2.2 范围不包含 row-level 鉴权。
func RequirePermission(code string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			perms, ok := PermissionsFromCtx(r)
			if !ok {
				writeForbidden(w, "auth_required", "authentication required")
				return
			}
			if !HasPermission(perms, code) {
				writeForbidden(w, "forbidden", "权限不足")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeForbidden 写 403 响应。JSON 形状与 apperr.AppError 一致。
//
// 这里不复用 apperr 包以避免 rbac → apperr 的循环依赖；同时保持中间件可
// 独立单测（不依赖 apperr 包内部）。
func writeForbidden(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"code":"` + code + `","message":"` + message + `"}`))
}