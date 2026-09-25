package rbac

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Role 角色记录。
type Role struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	IsSystem    bool      `json:"is_system"`
	Permissions []string  `json:"permissions"`
	UserCount   int       `json:"user_count,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Permission 权限码记录（参考表）。
type Permission struct {
	Code        string `json:"code"`
	Description string `json:"description"`
	GroupName   string `json:"group_name"`
}

// UserRole 用户—角色关联。
type UserRole struct {
	UserID    string    `json:"user_id"`
	RoleID    string    `json:"role_id"`
	TenantID  string    `json:"tenant_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Repo 是 RBAC 仓储（角色 + 用户角色分配）。
//
// 设计：所有方法接收 *sql.DB，业务层通过构造函数注入。租户隔离在
// ListByTenant / SetUserRoles 等方法中显式处理。
type Repo struct {
	DB *sql.DB
}

// NewRepo 构造 Repo。
func NewRepo(db *sql.DB) *Repo {
	return &Repo{DB: db}
}

// ErrRoleNotFound 角色不存在。
var ErrRoleNotFound = errors.New("rbac: role not found")

// ErrRoleIsSystem 试图删除 / 重命名系统预置角色。
var ErrRoleIsSystem = errors.New("rbac: role is system-protected")

// ----- Permission reference table -----

// ListPermissions 返回全部 25 个权限码（按 group 排序）。
func (r *Repo) ListPermissions(ctx context.Context) ([]Permission, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT code, description, group_name FROM permissions ORDER BY group_name, code`)
	if err != nil {
		return nil, fmt.Errorf("rbac: list permissions: %w", err)
	}
	defer rows.Close()
	out := make([]Permission, 0, 32)
	for rows.Next() {
		var p Permission
		if err := rows.Scan(&p.Code, &p.Description, &p.GroupName); err != nil {
			return nil, fmt.Errorf("rbac: scan permission: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rbac: iterate permissions: %w", err)
	}
	return out, nil
}

// ----- Role CRUD -----

// ListRoles 列出角色（可按 tenant 过滤；tenantID 空时返回全部）。
//
// 同时返回每个角色的 user_count（user_roles 表的关联计数）。
func (r *Repo) ListRoles(ctx context.Context, tenantID string) ([]Role, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if tenantID == "" {
		rows, err = r.DB.QueryContext(ctx,
			`SELECT r.id, r.tenant_id, r.name, r.description, r.is_system, r.permissions_json, r.created_at, r.updated_at,
			        COALESCE((SELECT COUNT(*) FROM user_roles ur WHERE ur.role_id = r.id), 0) AS user_count
			 FROM roles r ORDER BY r.is_system DESC, r.created_at ASC`)
	} else {
		rows, err = r.DB.QueryContext(ctx,
			`SELECT r.id, r.tenant_id, r.name, r.description, r.is_system, r.permissions_json, r.created_at, r.updated_at,
			        COALESCE((SELECT COUNT(*) FROM user_roles ur WHERE ur.role_id = r.id), 0) AS user_count
			 FROM roles r WHERE r.tenant_id = ? ORDER BY r.is_system DESC, r.created_at ASC`, tenantID)
	}
	if err != nil {
		return nil, fmt.Errorf("rbac: list roles: %w", err)
	}
	defer rows.Close()
	out := make([]Role, 0, 8)
	for rows.Next() {
		var (
			role      Role
			permsRaw  string
			isSystem  int
			createdAt int64
			updatedAt int64
		)
		if err := rows.Scan(&role.ID, &role.TenantID, &role.Name, &role.Description,
			&isSystem, &permsRaw, &createdAt, &updatedAt, &role.UserCount); err != nil {
			return nil, fmt.Errorf("rbac: scan role: %w", err)
		}
		role.IsSystem = isSystem == 1
		role.CreatedAt = time.UnixMilli(createdAt)
		role.UpdatedAt = time.UnixMilli(updatedAt)
		if permsRaw == "" {
			permsRaw = "[]"
		}
		if err := json.Unmarshal([]byte(permsRaw), &role.Permissions); err != nil {
			// 不让单条损坏的行拖垮整次 list；记录空数组并继续。
			role.Permissions = []string{}
		}
		out = append(out, role)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rbac: iterate roles: %w", err)
	}
	return out, nil
}

// GetRole 按 ID 取角色。找不到时返回 ErrRoleNotFound。
func (r *Repo) GetRole(ctx context.Context, id string) (*Role, error) {
	var (
		role      Role
		permsRaw  string
		isSystem  int
		createdAt int64
		updatedAt int64
	)
	err := r.DB.QueryRowContext(ctx,
		`SELECT id, tenant_id, name, description, is_system, permissions_json, created_at, updated_at
		 FROM roles WHERE id = ?`, id,
	).Scan(&role.ID, &role.TenantID, &role.Name, &role.Description, &isSystem, &permsRaw, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRoleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("rbac: get role: %w", err)
	}
	role.IsSystem = isSystem == 1
	role.CreatedAt = time.UnixMilli(createdAt)
	role.UpdatedAt = time.UnixMilli(updatedAt)
	if err := json.Unmarshal([]byte(permsRaw), &role.Permissions); err != nil {
		role.Permissions = []string{}
	}
	return &role, nil
}

// CreateRole 创建自定义角色。
//
// name 在同一 tenant 内必须唯一；permissions 不能为空数组（否则该角色
// 没有任何权限，毫无意义）；返回的 *Role 带生成的 id 与 created_at。
func (r *Repo) CreateRole(ctx context.Context, in Role) (*Role, error) {
	if in.TenantID == "" {
		in.TenantID = "tnt_default"
	}
	if in.Name == "" {
		return nil, errors.New("rbac: role name required")
	}
	if len(in.Permissions) == 0 {
		return nil, errors.New("rbac: role permissions must not be empty")
	}
	if unknown, ok := ValidatePermissionCodes(in.Permissions); !ok {
		return nil, fmt.Errorf("rbac: unknown permission codes: %v", unknown)
	}

	perms, err := json.Marshal(in.Permissions)
	if err != nil {
		return nil, fmt.Errorf("rbac: marshal permissions: %w", err)
	}
	now := time.Now().UnixMilli()
	// id 形式：role-<随机串>-<tenant>，避免与 seed 的固定 id 冲突。
	id := fmt.Sprintf("role-%d-%s", now, in.TenantID)

	_, err = r.DB.ExecContext(ctx,
		`INSERT INTO roles(id, tenant_id, name, description, permissions_json, is_system, created_at, updated_at)
		 VALUES(?, ?, ?, ?, ?, 0, ?, ?)`,
		id, in.TenantID, in.Name, in.Description, string(perms), now, now)
	if err != nil {
		return nil, fmt.Errorf("rbac: insert role: %w", err)
	}
	in.ID = id
	in.IsSystem = false
	in.UpdatedAt = time.UnixMilli(now)
	in.CreatedAt = time.UnixMilli(now)
	return &in, nil
}

// UpdateRole 更新自定义角色。
//
// is_system=1 的角色不允许改 name（保护系统预置）。permissions 校验同上。
func (r *Repo) UpdateRole(ctx context.Context, id string, in Role) (*Role, error) {
	existing, err := r.GetRole(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing.IsSystem && in.Name != "" && in.Name != existing.Name {
		return nil, ErrRoleIsSystem
	}
	if len(in.Permissions) > 0 {
		if unknown, ok := ValidatePermissionCodes(in.Permissions); !ok {
			return nil, fmt.Errorf("rbac: unknown permission codes: %v", unknown)
		}
	}

	// 用传入字段覆盖，未传字段保留原值。
	if in.Name != "" {
		existing.Name = in.Name
	}
	if in.Description != "" {
		existing.Description = in.Description
	}
	if len(in.Permissions) > 0 {
		existing.Permissions = in.Permissions
	}

	perms, err := json.Marshal(existing.Permissions)
	if err != nil {
		return nil, fmt.Errorf("rbac: marshal permissions: %w", err)
	}
	now := time.Now().UnixMilli()
	_, err = r.DB.ExecContext(ctx,
		`UPDATE roles SET name=?, description=?, permissions_json=?, updated_at=? WHERE id=?`,
		existing.Name, existing.Description, string(perms), now, id)
	if err != nil {
		return nil, fmt.Errorf("rbac: update role: %w", err)
	}
	existing.UpdatedAt = time.UnixMilli(now)
	return existing, nil
}

// DeleteRole 删除自定义角色。系统预置角色拒绝删除。
//
// 关联的 user_roles 行通过外键 ON DELETE CASCADE 自动清理。
func (r *Repo) DeleteRole(ctx context.Context, id string) error {
	existing, err := r.GetRole(ctx, id)
	if err != nil {
		return err
	}
	if existing.IsSystem {
		return ErrRoleIsSystem
	}
	res, err := r.DB.ExecContext(ctx, `DELETE FROM roles WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("rbac: delete role: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrRoleNotFound
	}
	return nil
}

// ----- User-Role assignment -----

// GetUserRoles 查某用户的所有角色（跨租户）。
func (r *Repo) GetUserRoles(ctx context.Context, userID string) ([]Role, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT r.id, r.tenant_id, r.name, r.description, r.is_system, r.permissions_json, r.created_at, r.updated_at
		 FROM roles r
		 JOIN user_roles ur ON ur.role_id = r.id
		 WHERE ur.user_id = ?
		 ORDER BY ur.created_at ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("rbac: get user roles: %w", err)
	}
	defer rows.Close()
	out := make([]Role, 0, 4)
	for rows.Next() {
		var (
			role      Role
			permsRaw  string
			isSystem  int
			createdAt int64
			updatedAt int64
		)
		if err := rows.Scan(&role.ID, &role.TenantID, &role.Name, &role.Description,
			&isSystem, &permsRaw, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("rbac: scan user role: %w", err)
		}
		role.IsSystem = isSystem == 1
		role.CreatedAt = time.UnixMilli(createdAt)
		role.UpdatedAt = time.UnixMilli(updatedAt)
		if err := json.Unmarshal([]byte(permsRaw), &role.Permissions); err != nil {
			role.Permissions = []string{}
		}
		out = append(out, role)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rbac: iterate user roles: %w", err)
	}
	return out, nil
}

// SetUserRoles 用 role_ids 列表替换某用户的角色分配。
//
// 实现：在事务内 DELETE + 批量 INSERT，原子完成。
//
// tenantID 可选；非空时所有插入行都带这个 tenant_id，留空则用第一个
// 角色的 tenant_id（便于跨租户 seed 的场景）。
func (r *Repo) SetUserRoles(ctx context.Context, userID, tenantID string, roleIDs []string) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rbac: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `DELETE FROM user_roles WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("rbac: delete user roles: %w", err)
	}

	now := time.Now().UnixMilli()
	for _, rid := range roleIDs {
		// 取该角色的 tenant_id（缺省值兜底）。
		var tID string
		err := tx.QueryRowContext(ctx, `SELECT tenant_id FROM roles WHERE id = ?`, rid).Scan(&tID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("rbac: role %s not found", rid)
		}
		if err != nil {
			return fmt.Errorf("rbac: lookup role tenant: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO user_roles(user_id, role_id, tenant_id, created_at) VALUES(?,?,?,?)`,
			userID, rid, tID, now); err != nil {
			return fmt.Errorf("rbac: insert user role: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("rbac: commit: %w", err)
	}
	return nil
}