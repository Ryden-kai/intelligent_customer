package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/audit"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/rbac"
)

// AdminRBAC 暴露 RBAC 管理端点（角色 CRUD + 权限字典 + 用户角色分配）。
//
// 7 个端点（全部要求对应权限码，详见 §5.1）：
//   GET    /api/admin/roles                   role.read
//   GET    /api/admin/roles/{id}              role.read
//   POST   /api/admin/roles                   role.manage
//   PUT    /api/admin/roles/{id}              role.manage
//   DELETE /api/admin/roles/{id}              role.manage
//   GET    /api/admin/permissions             role.read
//   GET    /api/admin/users/{user_id}/roles   role.manage
//   PUT    /api/admin/users/{user_id}/roles   role.manage
type AdminRBAC struct {
	Repo   *rbac.Repo
	Logger zerolog.Logger

	// AuditSink 可选；为空时不写审计日志。
	// 类型用 interface 避免循环依赖（audit 包会在 T02.3 落地）。
	AuditSink audit.Emitter
}

// ListRoles GET /api/admin/roles
func (h *AdminRBAC) ListRoles(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "admin.rbac.list_roles").Logger()
	tenantID := r.URL.Query().Get("tenant_id")
	if tenantID == "" {
		tenantID = "tnt_default"
	}
	roles, err := h.Repo.ListRoles(r.Context(), tenantID)
	if err != nil {
		lg.Error().Err(err).Msg("rbac_list_roles_failed")
		writeAppError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": roles,
		"total": len(roles),
	})
}

// GetRole GET /api/admin/roles/{id}
func (h *AdminRBAC) GetRole(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	role, err := h.Repo.GetRole(r.Context(), id)
	if err != nil {
		if errors.Is(err, rbac.ErrRoleNotFound) {
			writeAppError(w, r, apperr.NotFound("role not found"))
			return
		}
		writeAppError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, role)
}

// CreateRole POST /api/admin/roles
func (h *AdminRBAC) CreateRole(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "admin.rbac.create_role").Logger()
	var body struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
		TenantID    string   `json:"tenant_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}
	if body.Name == "" {
		writeAppError(w, r, apperr.BadRequest("name required"))
		return
	}
	role, err := h.Repo.CreateRole(r.Context(), rbac.Role{
		TenantID:    body.TenantID,
		Name:        body.Name,
		Description: body.Description,
		Permissions: body.Permissions,
	})
	if err != nil {
		// 把 repo 的 plain error 转成 4xx（业务校验）。
		writeAppError(w, r, classifyRBACError(err))
		return
	}
	// 审计。
	if h.AuditSink != nil {
		h.AuditSink.Emit(r.Context(), audit.ActionRoleCreate, "role", role.ID,
			audit.Payload{"name": role.Name, "permissions": role.Permissions})
	}
	lg.Info().Str("role_id", role.ID).Msg("rbac_role_created")
	writeJSON(w, http.StatusCreated, role)
}

// classifyRBACError 把 repo 层 plain error 翻译为 4xx apperr。
//
// 已知错误：
//   - "name required" / "permissions must not be empty" → 400
//   - "unknown permission codes: ..."                  → 400
//   - UNIQUE constraint (tenant_id, name)              → 409
//   - 其他                                              → 500
func classifyRBACError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "name required"),
		strings.Contains(msg, "permissions must not be empty"),
		strings.Contains(msg, "unknown permission codes"):
		return apperr.BadRequest(msg).WithCause(err)
	case strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "unique"):
		return apperr.Conflict(msg).WithCause(err)
	default:
		return err
	}
}

// UpdateRole PUT /api/admin/roles/{id}
func (h *AdminRBAC) UpdateRole(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "admin.rbac.update_role").Logger()
	id := chi.URLParam(r, "id")
	var body struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}
	role, err := h.Repo.UpdateRole(r.Context(), id, rbac.Role{
		Name:        body.Name,
		Description: body.Description,
		Permissions: body.Permissions,
	})
	if err != nil {
		if errors.Is(err, rbac.ErrRoleNotFound) {
			writeAppError(w, r, apperr.NotFound("role not found"))
			return
		}
		if errors.Is(err, rbac.ErrRoleIsSystem) {
			writeAppError(w, r, apperr.Forbidden("system role cannot be modified"))
			return
		}
		writeAppError(w, r, classifyRBACError(err))
		return
	}
	if h.AuditSink != nil {
		h.AuditSink.Emit(r.Context(), audit.ActionRoleUpdate, "role", role.ID,
			audit.Payload{"name": role.Name, "permissions": role.Permissions})
	}
	lg.Info().Str("role_id", role.ID).Msg("rbac_role_updated")
	writeJSON(w, http.StatusOK, role)
}

// DeleteRole DELETE /api/admin/roles/{id}
func (h *AdminRBAC) DeleteRole(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "admin.rbac.delete_role").Logger()
	id := chi.URLParam(r, "id")
	err := h.Repo.DeleteRole(r.Context(), id)
	if err != nil {
		if errors.Is(err, rbac.ErrRoleNotFound) {
			writeAppError(w, r, apperr.NotFound("role not found"))
			return
		}
		if errors.Is(err, rbac.ErrRoleIsSystem) {
			writeAppError(w, r, apperr.Forbidden("system role cannot be deleted"))
			return
		}
		writeAppError(w, r, err)
		return
	}
	if h.AuditSink != nil {
		h.AuditSink.Emit(r.Context(), audit.ActionRoleDelete, "role", id, nil)
	}
	lg.Info().Str("role_id", id).Msg("rbac_role_deleted")
	w.WriteHeader(http.StatusNoContent)
}

// ListPermissions GET /api/admin/permissions
//
// 用于前端权限矩阵初始化（前端按 group 渲染 checkbox 表）。
func (h *AdminRBAC) ListPermissions(w http.ResponseWriter, r *http.Request) {
	perms, err := h.Repo.ListPermissions(r.Context())
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"permissions": perms,
		"groups":      rbac.PermissionGroups,
		"total":       len(perms),
	})
}

// GetUserRoles GET /api/admin/users/{user_id}/roles
func (h *AdminRBAC) GetUserRoles(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	roles, err := h.Repo.GetUserRoles(r.Context(), userID)
	if err != nil {
		writeAppError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": userID,
		"roles":   roles,
		"total":   len(roles),
	})
}

// SetUserRoles PUT /api/admin/users/{user_id}/roles
func (h *AdminRBAC) SetUserRoles(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "admin.rbac.set_user_roles").Logger()
	userID := chi.URLParam(r, "user_id")
	var body struct {
		RoleIDs []string `json:"role_ids"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}
	tenantID := r.URL.Query().Get("tenant_id")
	if err := h.Repo.SetUserRoles(r.Context(), userID, tenantID, body.RoleIDs); err != nil {
		writeAppError(w, r, err)
		return
	}
	if h.AuditSink != nil {
		h.AuditSink.Emit(r.Context(), audit.ActionRoleAssign, "user", userID,
			audit.Payload{"role_ids": body.RoleIDs, "tenant_id": tenantID})
	}
	lg.Info().Str("user_id", userID).Int("roles", len(body.RoleIDs)).Msg("rbac_user_roles_set")
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": userID,
		"roles":   body.RoleIDs,
	})
}

// pageInt 解析 ?page=&size=，与上同 handler。
func pageInt(query, def string) int {
	v, _ := strconv.Atoi(query)
	if v <= 0 {
		v, _ = strconv.Atoi(def)
	}
	if v <= 0 {
		return 50
	}
	return v
}