// Package seed 提供 v2.2 PR1 的 RBAC / 权限 / 限流 预置数据。
//
// 本包独立于 internal/seed（后者负责 v1 时代的 FAQ + bootstrap admin）；
// 这里专门负责 v2.2 新增的 3 张表（roles / permissions / rate_limit_configs）。
//
// 所有 insert 都用 INSERT OR IGNORE，保证幂等：进程重启后第二次调用不会
// 重复插入（PRIMARY KEY / UNIQUE 兜底）。
package seed

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// PermissionSpec 是 permissions 表预置行的定义。
type PermissionSpec struct {
	Code        string // 'jev.template.read'
	Description string
	GroupName   string // 'jev' / 'conversation' / ...
}

// RoleSpec 是 roles 表预置行的定义。Permissions 是 JSON 序列化前的字符串切片。
type RoleSpec struct {
	Name        string
	Description string
	IsSystem    bool
	Permissions []string
}

// RateLimitSpec 是 rate_limit_configs 表预置行的定义。
type RateLimitSpec struct {
	Endpoint    string // 'POST:/api/auth/login'
	Dimension   string // 'ip' / 'tenant_id' / 'actor_id'
	PerMinute   int
	PerHour     int
	Burst       int
	Description string
}

// PermissionSeeds 是 v2.2 预置的 25 个权限码。来源：v2-ui-security-prd.md §7.1.2。
func PermissionSeeds() []PermissionSpec {
	return []PermissionSpec{
		// jev (5)
		{"jev.template.read", "查看 Jev 模板", "jev"},
		{"jev.template.write", "创建/编辑 Jev 模板", "jev"},
		{"jev.template.publish", "发布/归档 Jev 模板", "jev"},
		{"jev.decision.read", "查看 Jev 决策日志", "jev"},
		{"jev.decision.review", "复核 Jev 决策", "jev"},
		// conversation (4)
		{"conversation.read", "查看对话列表", "conversation"},
		{"conversation.write", "回复对话", "conversation"},
		{"conversation.delete", "删除对话", "conversation"},
		{"conversation.export", "导出对话", "conversation"},
		// skills (4)
		{"skills.read", "查看 Skills", "skills"},
		{"skills.create", "创建 Skill", "skills"},
		{"skills.toggle", "启停 Skill", "skills"},
		{"skills.delete", "删除 Skill", "skills"},
		// stats (2)
		{"stats.read", "查看统计", "stats"},
		{"stats.export", "导出统计", "stats"},
		// audit (2)
		{"audit.read", "查看审计日志", "audit"},
		{"audit.export", "导出审计日志", "audit"},
		// user (3)
		{"user.read", "查看用户", "user"},
		{"user.write", "创建/编辑用户", "user"},
		{"user.delete", "删除用户", "user"},
		// role (2)
		{"role.read", "查看角色", "role"},
		{"role.manage", "管理角色", "role"},
		// ratelimit (1)
		{"ratelimit.manage", "管理限流配置", "ratelimit"},
		// chat (2) — 用户端用
		{"chat.use", "使用聊天", "chat"},
		{"feedback.submit", "提交反馈", "feedback"},
	}
}

// RoleSeeds 是 v2.2 预置的 3 个角色：admin / agent / user。
// admin 用 ["*"] 标识全权限；其余按 PRD §7.1.1 列表。
func RoleSeeds() []RoleSpec {
	return []RoleSpec{
		{
			Name:        "admin",
			Description: "平台管理员（全部权限）",
			IsSystem:    true,
			Permissions: []string{"*"},
		},
		{
			Name:        "agent",
			Description: "客服坐席",
			IsSystem:    true,
			Permissions: []string{
				"conversation.read", "conversation.write",
				"skills.read",
				"stats.read",
			},
		},
		{
			Name:        "user",
			Description: "终端用户",
			IsSystem:    true,
			Permissions: []string{
				"chat.use", "feedback.submit",
			},
		},
	}
}

// RateLimitSeeds 是 v2.2 预置的 5 类限流配置。来源：v2-ui-security-prd.md §6.3.3。
func RateLimitSeeds() []RateLimitSpec {
	return []RateLimitSpec{
		{"POST:/api/auth/login", "ip", 10, 100, 5, "登录防撞库"},
		{"POST:/api/auth/register", "ip", 5, 20, 2, "注册防刷"},
		{"POST:/api/chat", "tenant_id", 60, 1000, 10, "对话发起"},
		{"POST:/api/feedback", "tenant_id", 30, 300, 5, "反馈提交"},
		{"ALL:/api/admin", "actor_id", 120, 2000, 20, "管理后台写操作"},
	}
}

// Result 是 RunRBACSeed 返回的结构，让 caller 知道插入了多少行。
type Result struct {
	PermissionsInserted int
	RolesInserted       int
	RateLimitsInserted  int
}

// RunRBACSeed 把 25 个 permission + 3 个 role + 5 个 rate_limit_configs 预置进 DB。
//
// 全部走 INSERT OR IGNORE：已存在（按主键 / UNIQUE）的行会被跳过，从而保证幂等。
//
// 调用方：cmd/server/main.go 在 db.Migrate 后、其它业务初始化前调用。
func RunRBACSeed(ctx context.Context, dbx *sql.DB) (Result, error) {
	out := Result{}
	now := time.Now().UnixMilli()

	// ---- 1. permissions ----
	{
		stmt, err := dbx.PrepareContext(ctx,
			`INSERT OR IGNORE INTO permissions(code, description, group_name, created_at)
			 VALUES(?,?,?,?)`)
		if err != nil {
			return out, err
		}
		for _, p := range PermissionSeeds() {
			res, err := stmt.ExecContext(ctx, p.Code, p.Description, p.GroupName, now)
			if err != nil {
				_ = stmt.Close()
				return out, err
			}
			if n, err := res.RowsAffected(); err == nil && n > 0 {
				out.PermissionsInserted++
			}
		}
		_ = stmt.Close()
	}

	// ---- 2. roles ----
	// 用角色名作为主键 ID 的派生（保证 system role id 稳定）。
	// 实际 id 用 uuid（允许后续自定义角色同名共存）。
	{
		stmt, err := dbx.PrepareContext(ctx,
			`INSERT OR IGNORE INTO roles(id, tenant_id, name, description, permissions_json, is_system, created_at, updated_at)
			 VALUES(?,?,?,?,?,?,?,?)`)
		if err != nil {
			return out, err
		}
		for _, r := range RoleSeeds() {
			perms, err := json.Marshal(r.Permissions)
			if err != nil {
				_ = stmt.Close()
				return out, err
			}
			// 角色 id 用 'role-<name>-<tenant>' 形式，保证 system role 幂等。
			id := "role-" + r.Name + "-tnt_default"
			res, err := stmt.ExecContext(ctx,
				id, "tnt_default", r.Name, r.Description,
				string(perms), boolInt(r.IsSystem), now, now)
			if err != nil {
				_ = stmt.Close()
				return out, err
			}
			if n, err := res.RowsAffected(); err == nil && n > 0 {
				out.RolesInserted++
			}
		}
		_ = stmt.Close()
	}

	// ---- 3. rate_limit_configs ----
	{
		stmt, err := dbx.PrepareContext(ctx,
			`INSERT OR IGNORE INTO rate_limit_configs(
				id, tenant_id, endpoint, dimension, per_minute, per_hour, burst, enabled, description, created_at, updated_at)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return out, err
		}
		for _, rl := range RateLimitSeeds() {
			id := "rlc-default-" + sanitizeID(rl.Endpoint) + "-" + rl.Dimension
			res, err := stmt.ExecContext(ctx,
				id, "tnt_default", rl.Endpoint, rl.Dimension,
				rl.PerMinute, rl.PerHour, rl.Burst, 1, rl.Description, now, now)
			if err != nil {
				_ = stmt.Close()
				return out, err
			}
			if n, err := res.RowsAffected(); err == nil && n > 0 {
				out.RateLimitsInserted++
			}
		}
		_ = stmt.Close()
	}

	return out, nil
}

// boolInt 把 Go bool 映射到 SQLite INTEGER（0/1）。
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// sanitizeID 把 'POST:/api/auth/login' 转成 'post-api-auth-login'，避免 id 中的特殊字符。
// 这里用纯字母数字，不做 hash，确保幂等；连续分隔符（':' 与 '/' 相邻时）会折叠为单个 '-'。
func sanitizeID(s string) string {
	out := make([]byte, 0, len(s))
	lastDash := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			out = append(out, c)
			lastDash = false
		case c >= 'A' && c <= 'Z':
			// 转小写
			out = append(out, c+32)
			lastDash = false
		case c >= '0' && c <= '9':
			out = append(out, c)
			lastDash = false
		case c == ':' || c == '/' || c == '-' || c == '_':
			if !lastDash {
				out = append(out, '-')
				lastDash = true
			}
		default:
			if !lastDash {
				out = append(out, '-')
				lastDash = true
			}
		}
	}
	return string(out)
}