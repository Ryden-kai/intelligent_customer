// Package rbac 实现 v2.2 PR2 的角色权限（RBAC）中间件与仓储。
//
// 设计要点（详见 docs/v2-architecture-design.md §3.4 + docs/v2-ui-security-prd.md §7）：
//
//  1. 权限码按 "{group}.{action}" 格式定义，共 25 个预置码（v2-ui-security-prd.md §7.1.2）。
//     全部码列在 AllPermissions 常量里；新增权限只需追加常量并 seed 到 permissions 表。
//
//  2. admin 角色用 "*" 通配符表示全权限；中间件对 "*" 短路放行。
//
//  3. 角色—权限的映射存在 roles.permissions_json（JSON 数组）。
//     v2.2 简化路径：暂不引入 role_permissions 多对多表（详见设计文档 §4.3）。
//
//  4. 中间件只对 /api/admin/* 子树生效；ctx 里通过 auth.PermissionsFromCtx 取权限列表，
//     校验失败返回 403。
//
//  5. 角色 / 用户角色分配的两个端点（PUT /api/admin/users/:id/roles、GET 同前缀），
//     与角色 CRUD 一起在本包 handler (backend/internal/handler/admin_rbac.go) 落地。
package rbac

// 权限码常量。来源：v2-ui-security-prd.md §7.1.2。
// 注意：与 internal/db/seed/rbac_seed.go 的 PermissionSeeds 必须保持一一对应，
// 否则 seed 阶段会重复插入同 code 但 description 不一致。
const (
	// jev (5)
	PermJevTemplateRead    = "jev.template.read"
	PermJevTemplateWrite   = "jev.template.write"
	PermJevTemplatePublish = "jev.template.publish"
	PermJevDecisionRead    = "jev.decision.read"
	PermJevDecisionReview  = "jev.decision.review"

	// conversation (4)
	PermConversationRead   = "conversation.read"
	PermConversationWrite  = "conversation.write"
	PermConversationDelete = "conversation.delete"
	PermConversationExport = "conversation.export"

	// skills (4)
	PermSkillsRead   = "skills.read"
	PermSkillsCreate = "skills.create"
	PermSkillsToggle = "skills.toggle"
	PermSkillsDelete = "skills.delete"

	// stats (2)
	PermStatsRead   = "stats.read"
	PermStatsExport = "stats.export"

	// audit (2)
	PermAuditRead   = "audit.read"
	PermAuditExport = "audit.export"

	// user (3)
	PermUserRead   = "user.read"
	PermUserWrite  = "user.write"
	PermUserDelete = "user.delete"

	// role (2)
	PermRoleRead   = "role.read"
	PermRoleManage = "role.manage"

	// ratelimit (1)
	PermRateLimitManage = "ratelimit.manage"

	// chat + feedback (2) — 用户端用
	PermChatUse        = "chat.use"
	PermFeedbackSubmit = "feedback.submit"
)

// PermWildcard 是 admin 角色的全权限通配符（与 seed.RoleSeeds 对齐）。
const PermWildcard = "*"

// AllPermissions 列出全部 25 个权限码。供前端权限矩阵初始化使用。
// 顺序与 v2-ui-security-prd.md §7.1.2 表格一致，便于 review。
var AllPermissions = []string{
	// jev
	PermJevTemplateRead, PermJevTemplateWrite, PermJevTemplatePublish,
	PermJevDecisionRead, PermJevDecisionReview,
	// conversation
	PermConversationRead, PermConversationWrite, PermConversationDelete, PermConversationExport,
	// skills
	PermSkillsRead, PermSkillsCreate, PermSkillsToggle, PermSkillsDelete,
	// stats
	PermStatsRead, PermStatsExport,
	// audit
	PermAuditRead, PermAuditExport,
	// user
	PermUserRead, PermUserWrite, PermUserDelete,
	// role
	PermRoleRead, PermRoleManage,
	// ratelimit
	PermRateLimitManage,
	// chat + feedback
	PermChatUse, PermFeedbackSubmit,
}

// PermissionGroups 把权限按 group 聚合，方便前端按 group 渲染矩阵。
var PermissionGroups = map[string][]string{
	"jev":          {PermJevTemplateRead, PermJevTemplateWrite, PermJevTemplatePublish, PermJevDecisionRead, PermJevDecisionReview},
	"conversation": {PermConversationRead, PermConversationWrite, PermConversationDelete, PermConversationExport},
	"skills":       {PermSkillsRead, PermSkillsCreate, PermSkillsToggle, PermSkillsDelete},
	"stats":        {PermStatsRead, PermStatsExport},
	"audit":        {PermAuditRead, PermAuditExport},
	"user":         {PermUserRead, PermUserWrite, PermUserDelete},
	"role":         {PermRoleRead, PermRoleManage},
	"ratelimit":    {PermRateLimitManage},
	"chat":         {PermChatUse, PermFeedbackSubmit},
}

// HasPermission 判断权限码 code 是否在用户权限集 perms 内。
//
// 规则：
//   - perms 含 "*" → 全权限通过。
//   - code 与 perms 任一元素相等 → 通过。
//   - 否则不通过。
//
// 注意：本函数不做通配符匹配（如 "jev.*"）。v2.2 的粒度仅到具体码；如未来需要
// group.* 通配符，扩展此函数即可，不影响调用方。
func HasPermission(perms []string, code string) bool {
	if code == "" {
		return false
	}
	for _, p := range perms {
		if p == PermWildcard {
			return true
		}
		if p == code {
			return true
		}
	}
	return false
}

// ValidatePermissionCodes 检查传入的权限码是否都在 AllPermissions 内。
// 用于角色创建 / 更新时的服务端校验（防拼错码，PRD R11 风险）。
//
// 返回 (unknown []string, ok bool)：unknown 为空时 ok=true。
func ValidatePermissionCodes(codes []string) (unknown []string, ok bool) {
	if len(codes) == 0 {
		return nil, true
	}
	known := make(map[string]struct{}, len(AllPermissions))
	for _, p := range AllPermissions {
		known[p] = struct{}{}
	}
	// Wildcard 单独放行。
	known[PermWildcard] = struct{}{}

	seen := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		if _, ok2 := known[c]; !ok2 {
			unknown = append(unknown, c)
		}
	}
	return unknown, len(unknown) == 0
}