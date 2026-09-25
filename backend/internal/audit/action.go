// Package audit 实现 v2.2 PR2 的审计日志写入、缓冲与查询。
//
// 设计要点（详见 docs/v2-architecture-design.md §3.5 + docs/v2-ui-security-prd.md §6.2）：
//
//  1. 写入走异步 channel（buffer 1000）+ 后台 goroutine flush；
//     主链路绝不阻塞（PRD R4 风险）。
//
//  2. 失败重试 3 次（指数退避 100ms / 500ms / 2s）；
//     最终失败落 logs/audit_failed.jsonl（每行一条 JSON），可由 ops 离线恢复。
//
//  3. 8 类 action 枚举在 action.go；调用方只引入常量，避免拼错。
//
//  4. 仓储提供 Insert / List / Get / ExportCSV 四类查询；
//     列表支持 from/to/actor/action/target_type 4 维筛选。
//
//  5. CSV 导出：UTF-8 BOM + RFC 4180；供 Excel 双击打开不乱码。
package audit

// Action 常量。命名规则：`{group}.{verb}`，全小写、`.` 分隔。
//
// 来源：v2-ui-security-prd.md §6.2.4 + §7.1.4 的枚举。
// 新增类型时在本文件追加常量，并在 admin handler 中埋点即可。
const (
	// Auth（2）
	ActionAuthLogin  = "auth.login"
	ActionAuthLogout = "auth.logout"

	// Role（4）— 角色 CRUD + 用户分配
	ActionRoleCreate = "role.create"
	ActionRoleUpdate = "role.update"
	ActionRoleDelete = "role.delete"
	ActionRoleAssign = "role.assign"

	// Template（4）— Jev 模板发布 / 归档 / 删除 / 创建
	ActionTemplateCreate   = "template.create"
	ActionTemplatePublish  = "template.publish"
	ActionTemplateArchive  = "template.archive"
	ActionTemplateDelete   = "template.delete"

	// Skills（3）
	ActionSkillCreate = "skills.create"
	ActionSkillToggle = "skills.toggle"
	ActionSkillDelete = "skills.delete"

	// Conversation（1）
	ActionConversationDelete = "conversation.delete"

	// Rate limit（1）— 限流配置变更
	ActionRateLimitConfigUpdate = "ratelimit.config.update"
)

// Payload 是 audit Log 调用的扩展字段载体，序列化为 payload_json。
//
// 字段命名使用 snake_case，便于离线工具解析。
type Payload map[string]any

// TargetType 合法值，调用方也可自由传入字符串。
// 仅文档化约定，不强制校验。
const (
	TargetUser         = "user"
	TargetRole         = "role"
	TargetTemplate     = "template"
	TargetSkill        = "skill"
	TargetConversation = "conversation"
	TargetRateLimit    = "ratelimit"
	TargetSession      = "session"
)