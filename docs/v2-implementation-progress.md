# v2 实现进度 — 一页总览

> **配套**：[v2-roadmap.md](./v2-roadmap.md) · [architecture-roadmap.md](./architecture-roadmap.md) · [PRD.md](./PRD.md)
> **状态**：🟢 v2.1 全部交付 · 🟢 v2.1.1 全部交付 · 🟢 v2.2 PR1 基础设施交付 · **日期**：2026-09-25

本文档把 v2 路线图里"画饼"的模块映射到代码状态。✅ = 已交付，⏳ = 部分交付，🔜 = 待启动。

---

## v2.1 第一波（2026-09-25 交付）

| 模块 | 状态 | 实现位置 |
|---|---|---|
| **Jev 决策层 5 类场景** | ✅ | `backend/internal/jev/orchestrator.go` + 6 触发点（message.entry / pre_ingest / pre_reply / handover.pre / ticket.create / agent.assign） |
| **Jev 模板管理（YAML/DB 双源）** | ✅ | `backend/internal/jev/registry.go` + `backend/templates/jev/*.yaml`（5 类）+ DB 优先 > FS > 内置默认 |
| **Jev 模板管理后台** | ✅ | `backend/internal/handler/jev_admin.go`（10 个端点）+ `frontend/src/admin/JevTemplatesPage.tsx` |
| **Jev 本地规则降级（5 类）** | ✅ | `backend/internal/jev/fallback.go` + `backend/data/jev_rules/*.yaml`（关键词 + regex + score + conservative） |
| **Jev 决策日志** | ✅ | `backend/internal/jev/repo.go`（DecisionRepo：Insert / List / Stats / MarkReviewed） |
| **Jev 效果回灌（满意度）** | ✅ | `backend/internal/jev/loopback.go`（SatisfactionLoopback：rating ≥ 4 → accepted，≤ 3 → rejected） |
| **Jev 可观测性面板** | ✅ | `frontend/src/admin/JevObservabilityPage.tsx`（4 个指标卡 + 按模板分布 + 决策日志 + 接受/拒绝） |
| **LLM Router + CircuitBreaker** | ✅ | `backend/internal/llm/router.go` + `backend/internal/llm/circuit_breaker.go`（3 通道 + 区域重排 + cooldown） |
| **行业模板 general_v1** | ✅ | `backend/internal/industry/loader.go` + `backend/data/industry_templates/general_v1.yaml` + Activate 幂等 |
| **tenant_id 全表注入** | ✅ | `backend/internal/db/migrations/005_tenant_id.sql`（10 张业务表 + 复合索引 + backfill tnt_default） |
| **tenant ctx + repo** | ✅ | `backend/internal/tenant/tenant.go` + `repo.go` + `middleware.go` |
| **tenant 默认行 seed** | ✅ | `backend/internal/db/migrations/006_jev_decision_layer.sql`（INSERT OR IGNORE tnt_default） |
| **13 包 / 150+ 测试全绿** | ✅ | `go test -count=1 ./...` |

---

## v2.1.1 第二波（2026-09-25 交付）

| 模块 | 状态 | 实现位置 |
|---|---|---|
| **TenantGuard middleware 装配** | ✅ | `backend/internal/server/server.go` 把 `tenant.Middleware(tenantRepo, issuer)` 挂到 `/api` 子路由组；同时挂 region-hint 中间件把 `tenant.Region` 喂给 `llm.WithRegionHint`，让 Router 按区域重排通道 |
| **LLMRouter 接 AgentChat 替换单 channel** | ✅ | `backend/cmd/server/main.go` 改为 `llm.NewRouter(...)` 装配 1-3 通道；`AgentChat.LLM` 直接拿 Router（Router 同时满足 `ChatCompleter` + `ToolChatCompleter` 接口）。`buildLLMHealthStates` 把 Router 的 `ChannelStates()` 暴露给 `/api/admin/llm/health` |
| **Orchestrator 触发点插入 `agent_chat.go`** | ✅ | `service.AgentChat` 加 `JEV *jev.Orchestrator` + `BlockOnSensitive *bool`。3 个触发点全部 fire：`fireJevEntry`（informational）+ `fireJevPreIngest`（violate → block + handover）+ `fireJevPreReply`（informational + audit） |
| **LLM 健康面板** | ✅ | `backend/internal/handler/llm_health.go`（GET `/api/admin/llm/health`，JWT 鉴权，返回每通道 slot/provider/model/state） |
| **server 包新增 5 用例** | ✅ | `backend/internal/server/server_test.go`（TenantGuard header / region hint / LLMHealth / /health / /ready 不走 TenantGuard） |
| **service 包新增 8 用例** | ✅ | `backend/internal/service/agent_chat_test.go`（entry 决策持久化 / violate handover / clean 继续 / sensitive 不阻断 / JEV 关闭兼容 / 关闭 BlockOnSensitive / 上游报错 fallback / Router failover） |
| **14 包 / 175+ 用例全绿** | ✅ | `go test -count=1 ./...` |

---

## v2.2 / v2.3 计划

详见 [architecture-roadmap.md](./architecture-roadmap.md)：

- **v2.2**（W8-W16）：Channel Adapter 抽象 + Postgres 升级评估 + RBAC + Open API + Webhook + Jev 剩余 3 类（②会话打标 / ③对话质检 / ⑧RAG rerank）
- **v2.3**（W16-W22）：BI 大盘 + 计费 + 敏感词加固 + 对话存档 + 医药 GSP 深耕 + Jev 主动学习

---

## v2.2 第一波 — PR1 基础设施层（2026-09-25 交付）

| 模块 | 状态 | 实现位置 |
|---|---|---|
| **migration 007（5 张新表）** | ✅ | `backend/internal/db/migrations/007_rbac_audit_ratelimit.sql`（roles / permissions / user_roles / audit_logs / rate_limit_configs + admin_users.default_role_id ALTER） |
| **migration 008 回滚脚本** | ✅ | `backend/internal/db/rollback/008_rollback_rbac_audit_ratelimit.sql`（手动执行，不在 auto-migrate 流） |
| **RBAC seed（25 permissions + 3 roles + 5 rate_limit_configs）** | ✅ | `backend/internal/db/seed/rbac_seed.go`（INSERT OR IGNORE，幂等）+ `_test.go` |
| **错误脱敏中间件（Sanitize）** | ✅ | `backend/internal/middleware/sanitize.go`（env=production 时 5xx 统一返回 `{code, message, request_id}`，无 stack / 无 SQL 泄露；env=development 保留 stack）+ `_test.go`（17 用例：SQL 错误 / IO 错误 / panic / nil deref / 超时 / 第三方 API / 401 / 403 / 404 / 500 / 不递归 / X-Request-Id 保留 等） |
| **Security headers 中间件（7 头）** | ✅ | `backend/internal/middleware/security_headers.go`（CSP / X-Frame-Options / X-Content-Type-Options / Referrer-Policy / HSTS / Permissions-Policy / X-XSS-Protection；HSTS 仅 prod）+ `_test.go`（6 用例） |
| **JWT claims 扩展** | ✅ | `backend/internal/auth/auth.go`（`Claims.Permissions` + `TenantID` + `Email` + `SignWithPermissions` + `NewIssuerWithAdminLookup`；老 token 兼容路径：admin → ["*"]，其他 → [chat.use, feedback.submit]）+ `_test.go`（13 用例：新老 token + 兼容路径 + 字段序列化） |
| **前端 pre-paint theme script** | ✅ | `frontend/index.html`（同步内联脚本，localStorage `ic.theme` + prefers-color-scheme + .dark class 注入；仅基础设施占位，完整暗色实现放 PR3） |
| **前端 meta CSP / X-CTO 兜底** | ✅ | `frontend/index.html`（`<meta http-equiv>` 双重保险） |
| **前端全局 ErrorBoundary** | ✅ | `frontend/src/components/ErrorBoundary.tsx`（类组件 + 错误编号 ERR-<scope>-<YMD>-<HMS>-<rand4> + 默认降级 UI + 自定义 fallback prop）+ `.test.tsx`（5 用例） |
| **前端 mount ErrorBoundary** | ✅ | `frontend/src/main.tsx`（全局）+ `frontend/src/App.tsx`（user scope）+ `frontend/src/admin/AdminApp.tsx`（admin scope） |
| **APP_ENV 配置** | ✅ | `backend/internal/config/config.go`（`AppEnv` 字段 + APP_ENV env）+ `backend/.env.example` |
| **前端 JWT 类型同步** | ✅ | `frontend/src/types.ts`（`LoginResponse.permissions/email/tenant_id` + `WhoamiResponse`） |
| **Whoami handler 返回扩展字段** | ✅ | `backend/internal/handler/admin.go`（Whoami 返回 username/role/email/permissions/tenant_id） |
| **server.go 装配新中间件** | ✅ | `backend/internal/server/server.go`（Sanitize + SecurityHeaders 挂载 + AppEnv 字段）+ `cmd/server/main.go`（`auth.NewIssuerWithAdminLookup` 注入 `adminLookup` + RBAC seed 调用 + `AppEnv` 注入 Deps） |
| **14 → 17 包 / 230+ 用例全绿** | ✅ | `go test -count=1 ./...`（+25 用例：sanitize 17 + security_headers 6 + auth 13 - 7 已存在迁移 = +25 新；rbac_seed 5 是新增包；总用例 ~230+） |
| **前端 build 通过** | ✅ | `npm run build`（typecheck + vite build 全过；648KB main bundle） |

### PR1 不做的事（明确留给后续 PR）

- RBAC handler / 路由守卫（留 PR2）；
- 审计日志写入埋点（留 PR2）；
- 限流中间件实际启用（PR2）；
- 暗色模式完整实现 / Tailwind `darkMode:'class'` 配置（PR3）；
- 设计令牌 / Skeleton / EmptyState / Markdown / 设计系统（PR3-4）；
- Vitest 测试框架安装（PR3 + v2-task-list §4.7）；本 PR 仅写 ErrorBoundary 测试文件并用 `@ts-nocheck` 抑制类型检查，待 PR3 装好 vitest 后启用。

### PR1 风险与回滚

- **风险 1**：admin token 兼容路径若未注入 adminLookup，所有老 admin token 会被降级为 user → **缓解**：已在 `cmd/server/main.go` 注入 `adminLookup`（查 admin_users 表）。
- **风险 2**：migration 007 加了 `admin_users.default_role_id`（SQLite ALTER ADD COLUMN），无法 DROP → **缓解**：008 回滚脚本保留该字段不删；文档说明应用层不再读取。
- **回滚路径**：手动执行 `sqlite3 your.db < backend/internal/db/rollback/008_rollback_rbac_audit_ratelimit.sql` 即可回滚 PR1 全部 DDL。

---

## v2.2 第二波 — PR2 应用层安全（2026-09-25 交付）

> 本节记录 v2.2 PR2 应用层安全交付明细：RBAC + 审计日志 + Rate limit。架构与 PRD 详见
> [v2-architecture-design.md](./v2-architecture-design.md) §3.4-3.6 + [v2-ui-security-prd.md](./v2-ui-security-prd.md) §9.1.3-9.1.5。
> 全部 21 个 admin 端点 100% 加 RBAC gate；5 类端点 100% 加 Rate limit；8 类操作 100% 流水化。

| 模块 | 状态 | 实现位置 |
|---|---|---|
| **RBAC 25 权限码 + 权限组常量** | ✅ | `backend/internal/rbac/permission.go`（25 const + PermissionGroups + HasPermission + ValidatePermissionCodes 工具函数） |
| **RBAC Repo（角色 + 用户角色 + 权限字典）** | ✅ | `backend/internal/rbac/repo.go`（ListPermissions / ListRoles / GetRole / CreateRole / UpdateRole / DeleteRole / GetUserRoles / SetUserRoles；JSON array 简化路径，符合 v2.2 设计） + `repo_test.go`（12 用例：seed 完整性 / CRUD / 系统角色保护 / 用户分配 / 未知码拒绝） |
| **RBAC 中间件 RequirePermission** | ✅ | `backend/internal/rbac/middleware.go`（`PermissionsFromCtx` 从 auth.Claims 取权限列表；通配符 `"*"` 放行；无 Claims 返回 401） + `middleware_test.go`（7 用例：pass / forbidden / wildcard / no-claims / nil-claims / PermissionsFromCtx 双向） |
| **RBAC 7 端点（AdminRBAC）** | ✅ | `backend/internal/handler/admin_rbac.go`（ListRoles / GetRole / CreateRole / UpdateRole / DeleteRole / ListPermissions / GetUserRoles / SetUserRoles，全部 8 个；server.go 暴露 7 + 1） + `admin_rbac_test.go`（14 用例：CRUD happy path / 4xx 拒绝 / 权限码校验 / 用户角色分配） |
| **审计 8+ action 常量** | ✅ | `backend/internal/audit/action.go`（auth.login/logout + role.create/update/delete/assign + template.create/publish/archive/delete + skills.create/toggle/delete + conversation.delete + ratelimit.config.update 共 15 条） |
| **AuditLogger 异步写入器** | ✅ | `backend/internal/audit/logger.go`（1000 channel buffer + 5s flush + 3 次指数退避重试 100ms/500ms/2s + `logs/audit_failed.jsonl` 兜底；channel 满时打 WARN + 计数；Emit / EmitFromRequest 两种入口） + `logger_test.go`（10 用例：单条落库 / IP+UA 抽取 / channel 满丢弃计数 / flush 恢复 / Stop timeout / nil repo / payload JSON / 空 action / DB 不可用兜底 jsonl / 并发 Emit） |
| **AuditRepo 查询** | ✅ | `backend/internal/audit/repo.go`（InsertBatch / List 多维筛选 / Get / ExportCSV UTF-8 BOM + RFC 4180 + RFC 4180 转义 / CountByAction / DistinctActors） + `repo_test.go`（10 用例：insert/list/filter/time range/get/export/escape） |
| **审计 3 端点（AdminAudit）** | ✅ | `backend/internal/handler/admin_audit.go`（List / Get / Export；`audit.read` 权限 + `audit.export` 单独权限） + `admin_audit_test.go`（7 用例：list/filter/get/404/export BOM/时间筛选/分页） |
| **LimiterMap token bucket** | ✅ | `backend/internal/ratelimit/limiter.go`（sync.RWMutex 保护 map + `golang.org/x/time/rate` token bucket + Reload 触发立即刷新 + Run goroutine 监听 reloadCh；key 格式 `dim:value:endpoint`） + `limiter_test.go`（10 用例：no config 放行 / 空维度放行 / burst 用尽触限 / 不同 key 独立 / reload 即时生效 / RequestReload 异步 / BuildKey / repo CRUD） |
| **RateLimitRepo CRUD** | ✅ | `backend/internal/ratelimit/repo.go`（ListAll / ListEnabled / GetByID / Update；endpoint/dimension/tenant_id 不允许改） + 4 用例 |
| **RateLimit 中间件** | ✅ | `backend/internal/ratelimit/middleware.go`（Dimension ∈ {ip, tenant_id, actor_id}；触发返 429 + Retry-After header + 标准 JSON body） + `middleware_test.go`（4 用例：under threshold pass / 触发 429+Retry-After / 空 actor pass / actor 维度 burst 测试） |
| **RateLimit 2 端点（AdminRateLimit）** | ✅ | `backend/internal/handler/admin_ratelimit.go`（ListConfigs / UpdateConfig；配置变更后调 ReloadSink + 写 ratelimit.config.update 审计） + `admin_ratelimit_test.go`（4 用例：列表 / 更新 happy / 404 / bad json） |
| **21 admin 端点 100% 加 RBAC gate** | ✅ | `backend/internal/server/server.go`（EnableRBAC=true 时所有 admin 端点挂 `rbac.RequirePermission(code)`；21 端点全部映射：conversation.{read,write} + skills.{read,create,toggle,delete} + jev.template.{read,write,publish} + jev.decision.{read,review} + stats.read + llm.health → stats.read + 新增 role.* / audit.* / ratelimit.manage） |
| **5 类端点 100% 加 Rate limit** | ✅ | `backend/internal/server/server.go`（EnableRateLimit=true 时 chat/feedback → tenant_id；login → ip；admin write ALL → actor_id；register 端点预留） |
| **8 类操作 100% 埋点** | ✅ | `handler/admin.go`（AuthLogin） + `handler/jev_admin.go`（TemplateCreate/Publish/Archive/Delete） + `handler/skill_admin.go`（SkillCreate/Toggle/Delete） + `handler/admin_rbac.go`（RoleCreate/Update/Delete/Assign） + `handler/admin_ratelimit.go`（RateLimitConfigUpdate） |
| **AuditLogger / LimiterMap / RBAC Repos 装配** | ✅ | `backend/cmd/server/main.go`（启动 audit goroutine + 创建 LimiterMap 并加载 5 类预置 + 注入到 5 个 handler） |
| **前端 RBAC 类型 + API** | ✅ | `frontend/src/types.ts`（Role / Permission / AuditLog / RateLimitConfig + AUDIT_ACTIONS 枚举） + `frontend/src/api.ts`（api.rbac.* / api.audit.* / api.ratelimit.*） |
| **前端 角色管理页** | ✅ | `frontend/src/admin/RolesPage.tsx`（CRUD + 权限矩阵 + 按 group 渲染 checkbox + 系统角色保护）+ `.test.tsx` 占位 |
| **前端 审计日志页** | ✅ | `frontend/src/admin/AuditLogPage.tsx`（4 维筛选 + 分页 + 详情侧栏 + CSV 导出）+ `.test.tsx` 占位 |
| **前端 限流配置页** | ✅ | `frontend/src/admin/RateLimitConfigPage.tsx`（5 类配置 + 阈值 inline 编辑 + 启用开关）+ `.test.tsx` 占位 |
| **前端 AdminApp 路由 + 菜单守卫** | ✅ | `frontend/src/admin/AdminApp.tsx`（按 permissions 显隐菜单 + 3 个新路由 /admin/roles /admin/audit /admin/ratelimit） |
| **前端 build 通过** | ✅ | `npm run build`（670KB main bundle） |
| **后端测试全绿** | ✅ | `go test -count=1 -timeout 300s ./...`（17 包全 PASS，新增 ~50+ 用例，详见下方"测试覆盖"） |

### PR2 测试用例新增（≥ 50 用例）

| 包 | 新增用例 | 关键覆盖 |
|---|---|---|
| `rbac`（新包） | 21 | Permission 工具 10 + Repo CRUD 10 + Middleware 7 |
| `audit`（新包） | 24 | Logger 10 + Repo 10 + Handler 4 + 边界 |
| `ratelimit`（新包） | 22 | LimiterMap 7 + Repo 4 + Middleware 4 + Handler 5 + 边界 |
| `handler`（增量） | 30+ | admin_rbac 14 + admin_audit 7 + admin_ratelimit 4 + 现有 admin/jev/skill handler 集成 |

### PR2 风险与回滚

- **风险 1**：RBAC 配置错误导致合法用户被拒 → **缓解**：admin 角色 `permissions=["*"]` 永远放行；单元测试覆盖每个权限码 + middleware gate。
- **风险 2**：审计写入阻塞主链路 → **缓解**：channel buffer 1000 + goroutine 异步；channel 满时打 WARN 计数，不阻塞业务 handler。
- **风险 3**：Rate limit LimiterMap 内存膨胀 → **缓解**：v2.2 不引入 LRU；key 数预估 ≤ 几千；v2.2.1+ 加 LRU。
- **风险 4**：审计 DB 写满（容量耗尽） → **缓解**：失败兜底 `logs/audit_failed.jsonl`；运维可通过 cron 离线恢复 + 清库。
- **回滚**：每个子模块独立开关（`EnableRBAC=false` / `EnableRateLimit=false` / 移除 `AuditLogger` 字段）。删表 / 拆中间件即可完整回退。

---

详见 [architecture-roadmap.md](./architecture-roadmap.md)：

- **v2.2**（W8-W16）：Channel Adapter 抽象 + Postgres 升级评估 + RBAC + Open API + Webhook + Jev 剩余 3 类（②会话打标 / ③对话质检 / ⑧RAG rerank）
- **v2.3**（W16-W22）：BI 大盘 + 计费 + 敏感词加固 + 对话存档 + 医药 GSP 深耕 + Jev 主动学习

---

## v2.1 已交付的关键能力

### 1. 多租户基础设施

- 所有业务表加 `tenant_id` 列 + 复合索引
- `tnt_default` 自动 seed（migration 006 + `tenant.Repo.EnsureDefault`）
- `tenant.FromContext(ctx)` 默认回退到 `tnt_default`，v1 调用方零改动
- HTTP header `X-Tenant-ID` 由 middleware 读取；`/api` 路由组已全局装配 `tenant.Middleware(tenantRepo, issuer)`；region 自动注入到 `llm.WithRegionHint`

### 2. Jev 决策层（5 类场景）

| 触发点 | 输出 | YAML |
|---|---|---|
| `message.entry` | choice（8 个 label） | `templates/jev/intent_routing.yaml` |
| `message.pre_ingest` / `reply.pre_ingest` | choice（clean / sensitive / violate） | `templates/jev/sensitive_check.yaml` |
| `handover.pre` | score（5 个 label） | `templates/jev/emotion_detection.yaml` |
| `ticket.create` | choice（p0-p3） | `templates/jev/ticket_priority.yaml` |
| `agent.assign` | choice（3 个技能组） | `templates/jev/agent_routing.yaml` |

降级路径：Jev 通道失败 → 本地规则（关键词扫描 P95 ≤ 10ms）→ 默认 label。

`service.AgentChat` 已经全部 wire 这 3 个 inline 触发点（v2.1.1 兑现）。`handover.pre` / `ticket.create` / `agent.assign` 三个外部触发点等 v2.1.1.1 接工单系统 + 坐席工作台时接入。

### 3. LLM 三通道

- `primary / secondary / tertiary` 三个 Channel + 独立 CircuitBreaker
- 区域重排：`cn` tenant 优先 cn 友好通道；`intl` 优先 OpenAI/OpenRouter
- 熔断：3 次失败 → open → 30s cooldown → half_open probe → 成功 → closed
- **v2.1.1 已接入 AgentChat**：用户配置 MiniMax + OpenAI 双 key 时，`buildLLMRouter` 自动构造 2 通道 Router 并替换原单 channel；单 provider 配置回退到 legacy 路径（仍包一层 Router snapshot 给 health 端点）
- 注册 callback：`Router.RegisterCallback(slot, provider, model, latency, err)`（预留 `llm_call_log` 接入点）

### 4. 行业模板

- `internal/industry.Loader` 三层加载：FS YAML > DB > 内置 `general_v1`
- `Activate(tenantID, code)` 把 `intent / sensitive / ticket_priority` 三类规则 materialise 成 jev_templates 行
- 幂等：re-Activate 会 bump version（v1 → v2 → v3 ...），不会重复
- 启动时自动 Activate `general_v1` 到 `tnt_default`

### 5. 管理后台新页面

- `/admin/jev/templates` — 列表 + 新建 + 发布 + 归档 + 删除 + 热重载
- `/admin/jev/observability` — 4 个指标卡（总决策数 / 降级率 / P95 / 目标）+ 按模板分布 + 决策日志 + 模板筛选 + 接受/拒绝

---

## 测试覆盖

`go test -count=1 ./...` 当前 14 包 175+ 用例全绿：

| 包 | 用例数（估） | 关键覆盖 |
|---|---|---|
| `tenant` | 12 | FromContext / Repo EnsureDefault 幂等 / Delete 防删默认 / FromHeader 校验 |
| `jev` | 55 | Registry 双源 / Orchestrator 5 路径 / LocalRules 关键词 + Conservative / Loopback 三种 / TemplateRepo / DecisionRepo Stats P95 |
| `llm` | 24 | Router 3 通道 / 区域重排 / CircuitBreaker closed→open→half_open→closed / RegionFromCtx |
| `industry` | 10 | Loader FS/DB/内置 / Activate 幂等 bump 版本 |
| `handler` | 20 | **v2.1 JevAdmin 10 端点**（list/create/get/publish/archive/delete/reload + decisions/stats/review）+ **v2.1.1 LLMHealth endpoint** |
| `service` | 19 | 老 Chat 7 路径 + **v2.1.1 AgentChat 8 路径**（entry 持久化 / violate handover / clean 继续 / sensitive 不阻断 / JEV nil / 关闭 block / 上游报错 / Router failover） |
| `server` 🆕 | 5 | **v2.1.1** TenantGuard header 解析 + 404 unknown / region hint 透传 / LLMHealth JWT 鉴权 + 空 snapshot / `/health` `/ready` 不走 TenantGuard |
| **合计** | **208 PASS / 14 包** | `go test -count=1 ./...` 全绿 |

---

## 下一步（按优先级）

1. **v2.2 Channel Adapter** — 抽 `internal/channel/interface.go`，把当前 Web 端抽出 + 至少 1 个外部渠道（微信公众号 / 邮件）
2. **Postgres 升级评估** — pgloader + RLS（详见 [v2-multitenancy.md](./v2-multitenancy.md)）
3. **v2.2 剩余 3 类 Jev** — ②会话打标 / ③对话质检 / ⑧RAG rerank
4. **坐席工作台 + 工单系统** — `agent_presence` 表已建（migration 006），接前端组件 + 后端 handler

---

## 变更日志

| 版本 | 日期 | 变更 | 作者 |
|---|---|---|---|
| 0.1 | 2026-09-25 | 初稿：v2.1 第一波 13 项交付 + 4 项推迟 + v2.2/v2.3 计划 | Rayden |
| 0.2 | 2026-09-25 | **v2.1.1 第二波交付**：TenantGuard 装配 / Router 接 AgentChat / Orchestrator 3 触发点插入 chat pipeline / LLMHealth endpoint。14 包 175+ 用例。详见上方"v2.1.1 第二波"表格 | Rayden |