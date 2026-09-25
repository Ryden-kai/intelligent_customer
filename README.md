# 智能客服 Agent（Tool Calling 版）

一个真正能**自主调用工具**的客服 Agent：Tool Calling + 有界循环 + 三种 skill 形态（硬编码 + 文件系统热加载 + SQLite CRUD）+ 退款二次确认 + 流式步骤可视化。

不再是固定工作流（FAQ→意图→LLM），而是 LLM 看着工具列表自己决定调哪个、几时停、要不要转人工。

```
.
├── backend/         # Go API 服务（chi + SQLite + Agent + Skill）
├── frontend/        # 纯静态 SPA（React + Vite + TS），适合 Cloudflare Pages
├── docs/            # PRD / 架构 / ADR / 路线图（详见 docs/README.md）
├── README.md        # ← 你在这里
├── .gitignore
└── .workbuddy/      # 本地工作记忆（不入业务）
```

---

## 架构升级概览

| 维度 | v1（agent 已交付） | v2.1 + v2.1.1（新增） |
|---|---|---|
| 决策方式 | 自主循环（LLM 决定调哪个 tool） | + **Jev 决策层**（5 类：意图/敏感词/情绪/工单优先级/坐席分配）+ 失败本地规则降级 + **6 触发点插入 chat pipeline（v2.1.1）** |
| 工具调用 | Tool Calling（OpenAI 原生 / Anthropic JSON fallback） | + 6 触发点插入 Orchestrator |
| Skill 来源 | 三种：硬编码 + `data/skills/*.json` + SQLite CRUD | + 行业模板 YAML 一键 Activate |
| 写操作保护 | 退款/取消等强制 human-in-the-loop | 沿用 |
| 多租户 | 单租户 | + **tenant_id** 全表注入 + `tnt_default` 自动 backfill + FromContext 默认回退 + **TenantGuard middleware 全局装配（v2.1.1，`X-Tenant-ID` header / JWT.tid 解析 + 区域 hint 透传给 LLM Router）** |
| LLM 通道 | 单 provider 直连 | + **三通道 Router**（primary/secondary/tertiary + CircuitBreaker + 区域重排）+ **Router 接 AgentChat（v2.1.1）+ `/api/admin/llm/health` 健康面板** |
| 效果回灌 | 无 | + `jev_decisions` 日志 + 满意度反哺（人工标注 v2.3 闭环） |
| 文档 | PRD v1.0 + 架构总览 | + **9 份 v2 设计文档**（PRD-级）+ 7 份 ADR（ADR-002 / 006 / 007 新增） |
| 前端 | 聊天 + 5 个后台页 | + **Jev 模板管理页** + **Jev 可观测页**（4 个指标卡 + 按模板分布 + 决策日志 + 接受/拒绝） |
| trace_id | HTTP → service → agent → LLM/skill 全链路 | + 贯穿 jev_decisions.trace_id（O11y 关联） |

后端仍是单一 Go binary；前端仍可直传 Cloudflare Pages。两个工程独立部署。

---

## 快速开始

### 后端（监听 :8080）

```bash
cd backend
cp .env.example .env        # 填 JWT_SECRET、LLM key 等
bash scripts/build.sh
bash scripts/run.sh         # → http://localhost:8080/health
```

### 前端（监听 :5173）

```bash
cd frontend
npm install
npm run dev                 # → http://localhost:5173
```

浏览器打开：
- `/` — 聊天界面（流式步骤展示）
- `/admin` — 管理后台（默认 `admin/admin123`）
  - `/admin/conversations` 会话列表
  - `/admin/stats` 满意度
  - `/admin/skills` Skill CRUD
  - **`/admin/jev/templates` v2.1 Jev 模板管理**
  - **`/admin/jev/observability` v2.1 Jev 决策可观测**
  - **`/admin/roles` v2.2 PR2 角色管理**（CRUD + 权限矩阵）
  - **`/admin/audit` v2.2 PR2 审计日志**（4 维筛选 + 详情 + CSV 导出）
  - **`/admin/ratelimit` v2.2 PR2 限流配置**（5 类端点阈值实时调整）

---

## Skill 系统（核心新功能）

Agent 决策时看到的工具列表来自 `skill.Registry`，三种来源合并：

| 来源 | 路径 | 适用场景 |
|---|---|---|
| Built-in | `internal/skill/registry.go:RegisterBuiltins` | 关键 + 写操作（退款/取消）必须内置 |
| Filesystem | `backend/data/skills/*.json` | 运营配置热加载（改 JSON + reload endpoint 立即生效） |
| Database | SQLite `skills` 表 | 审计 + 后台 CRUD 增删改查 |

**安全约束**：
- 动态 skill（fs + db）强制 `ReadOnly=true` + `RequiresHuman=false`
- 写操作必须硬编码 — 不能通过 admin CRUD 创建退款 skill
- 用 `bash scripts/demo.sh` 一键演示三种来源 + 退款 happy path

### 已内置的 3 个 skill

| name | 类型 | 说明 |
|---|---|---|
| `query_order` | readonly | 查订单状态、物流（mock `mock_orders` 表） |
| `query_coupon` | readonly | 查可用优惠券 + 积分余额 |
| `apply_refund` | **mutation + human** | 创建待确认退款工单；前端确认后调 `/api/skills/confirm` 才落库 |

### 退款二次确认流程

```
用户："我想退款 ORD-1001"
  ↓ LLM 决定调 apply_refund
  ↓ apply_refund: 创建 skill_pending_tickets(id, status='pending')
  ↓ agent 收到 ExecutionResult{Status: pending_human}
  ↓ 流式输出 {"type":"pending_human","ticketId":"..."}
前端弹确认面板
用户点"确认" → POST /api/skills/confirm {ticketId, userId}
  ↓ Tickets.Confirm(): 事务 + 行锁 + 状态校验
  ↓ mock_orders.status='refunding'
  ↓ ticket.status='confirmed'
```

5 分钟未确认自动 expired（后台 `TicketSweeper` 进程每分钟跑一次 `SweepExpired`，间隔可由 `TICKET_SWEEP_INTERVAL` env 调整）。

---

## Skill 开发指南

详见 [`backend/data/skills/README.md`](backend/data/skills/README.md)：
- `echo` handler：开发调试用
- `http` handler：POST 到 URL，5s 超时
- 字段说明 + 安全约束

新加 builtin skill：`internal/skill/registry.go` 加 `Definition{...}`，review 后编译进 binary。

---

## Agent 循环配置

所有参数走 env（`backend/.env`）：

| 变量 | 默认 | 说明 |
|---|---|---|
| `AGENT_MAX_STEPS` | 5 | 自主循环步数上限 |
| `AGENT_MAX_TOKENS` | 4000 | 累计 token 预算 |
| `AGENT_MAX_WALLCLOCK` | 20s | 整个 run 的墙钟 |
| `AGENT_SKILL_TIMEOUT` | 5s | 单个 skill 执行超时 |
| `AGENT_SYSTEM_PROMPT` | "" | 自定义 system prompt；留空用默认 |

任一上限触发 → 转人工 + reason（`max_steps` / `max_tokens` / `max_wallclock` / `llm_error`）。

---

## 评测

### 20 条用例的 eval 集

```bash
cd backend
export OPENAI_API_KEY="sk-..."      # 或 MINIMAX_API_KEY
export OPENAI_BASE_URL="https://api.openai.com/v1"   # 或 minimaxi.cn
bash scripts/eval.sh
```

`scripts/eval.jsonl` 覆盖：normal / boundary / adversarial / recovery / performance 五类。

### 不依赖 LLM 的 demo 闭环（推荐先跑这个）

```bash
cd backend
bash scripts/demo.sh
```

自动跑完：
1. 起服务（用不可达 LLM URL → 触发降级路径，**不影响 demo**）
2. 登录拿 JWT
3. 热加载 `data/skills/*.json`
4. seed 一个 readonly skill 进 SQLite + reload
5. 调 `apply_refund` 拿 ticket → `POST /api/skills/confirm` → 验证 `mock_orders.status='refunding'`
6. 再开一个 ticket 走 cancel 路径

全过 < 5 秒，验证三种 skill 形态 + 退款闭环。

---

## 接口摘要

公开：

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/chat` | 发消息，返回 **NDJSON 流**（step / tool_call / tool_result / final / handover / done） |
| POST | `/api/feedback` | 满意度（1-5 + 评论） |
| POST | `/api/skills/confirm` | 确认待执行的 skill（退款二次确认） |
| POST | `/api/skills/cancel` | 取消待执行的 skill ticket |

后台（JWT）：

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/admin/login` | admin/admin123 |
| GET | `/api/admin/me` | |
| GET | `/api/admin/conversations` | 会话列表 |
| GET | `/api/admin/conversations/{id}` | 会话详情（含 **skill_invocations** 审计） |
| POST | `/api/admin/conversations/{id}/reply` | 人工回复 |
| GET | `/api/admin/stats/satisfaction` | 满意度统计 |
| GET | `/api/admin/skills` | skill 列表（含 builtin/fs/db 元数据） |
| POST | `/api/admin/skills` | 新建 dynamic skill |
| PUT | `/api/admin/skills/{id}` | 更新 |
| DELETE | `/api/admin/skills/{id}` | 删除 |
| POST | `/api/admin/skills/reload` | 热加载 fs + db skill |
| **GET / POST / DELETE** | **`/api/admin/jev/templates[/{id}]`** | **v2.1 Jev 模板 CRUD** |
| **POST** | **`/api/admin/jev/templates/publish/{id}`** | **v2.1 模板发布（写审计）** |
| **POST** | **`/api/admin/jev/templates/archive/{id}`** | **v2.1 模板归档（写审计）** |
| **GET** | **`/api/admin/jev/decisions`** | **v2.1 决策列表** |
| **GET** | **`/api/admin/jev/decisions/{id}`** | **v2.1 决策详情** |
| **POST** | **`/api/admin/jev/decisions/{id}/review`** | **v2.1 复核决策（写审计）** |
| **GET** | **`/api/admin/jev/stats`** | **v2.1 决策统计** |
| **GET** | **`/api/admin/llm/health`** | **v2.1.1 LLM 三通道健康** |
| **GET** | **`/api/admin/roles`** | **v2.2 PR2 角色列表（需 `role.read`）** |
| **GET** | **`/api/admin/roles/{id}`** | **v2.2 PR2 角色详情** |
| **POST** | **`/api/admin/roles`** | **v2.2 PR2 新建角色（需 `role.manage` + 写审计）** |
| **PUT** | **`/api/admin/roles/{id}`** | **v2.2 PR2 更新角色** |
| **DELETE** | **`/api/admin/roles/{id}`** | **v2.2 PR2 删除自定义角色（系统预置不可删）** |
| **GET** | **`/api/admin/permissions`** | **v2.2 PR2 25 个权限码字典** |
| **GET** | **`/api/admin/users/{user_id}/roles`** | **v2.2 PR2 用户角色查询** |
| **PUT** | **`/api/admin/users/{user_id}/roles`** | **v2.2 PR2 用户角色分配（写审计）** |
| **GET** | **`/api/admin/audit`** | **v2.2 PR2 审计列表（需 `audit.read`）** |
| **GET** | **`/api/admin/audit/{id}`** | **v2.2 PR2 审计详情** |
| **GET** | **`/api/admin/audit/export`** | **v2.2 PR2 CSV 导出（需 `audit.export`；UTF-8 BOM）** |
| **GET** | **`/api/admin/ratelimit/configs`** | **v2.2 PR2 限流配置列表（需 `ratelimit.manage`）** |
| **PUT** | **`/api/admin/ratelimit/configs/{id}`** | **v2.2 PR2 更新阈值（写审计 + 立即生效）** |
| **POST** | **`/api/admin/jev/templates/publish\|archive/{id}`** | **v2.1 模板状态翻转** |
| **POST** | **`/api/admin/jev/templates/reload`** | **v2.1 热重载模板** |
| **GET** | **`/api/admin/jev/decisions`** | **v2.1 决策日志** |
| **POST** | **`/api/admin/jev/decisions/{id}/review`** | **v2.1 接受 / 拒绝** |
| **GET** | **`/api/admin/jev/stats`** | **v2.1 7 天窗口 stats** |
| **GET** | **`/api/admin/llm/health`** | **v2.1.1 LLM Router 熔断器快照**（每通道 slot/provider/model/state） |

---

## LLM 通道

两个实现都满足 `llm.ToolChatCompleter`（agent 路径）和 `llm.ChatCompleter`（兼容）：

- **OpenAI 兼容**（OpenAI、OpenRouter 等）：用原生 `openai.ChatCompletionRequest.Tools`
- **Anthropic 协议**（minimax cn）：JSON 指令 fallback（system prompt 塞工具列表 + 要求 JSON 输出）

切换走 `LLM_PROVIDER=openai|MiniMax` + 对应 API key + base URL。

✅ MiniMax cn（`api.minimaxi.cn/anthropic`）走 Anthropic 协议，**目前可直接使用**。如需切换到 OpenAI 兼容通道：`OPENAI_BASE_URL=https://api.openai.com/v1` + `OPENAI_API_KEY`。

---

## 应用层安全

- **接口签名**：HMAC-SHA256(secret, canonical)，header `X-Timestamp/X-Nonce/X-Signature`
  - canonical = `METHOD\nPATH\nTS\nNONCE\nsha256-hex(body)`
  - 时间窗 ±5 分钟；nonce 用 SQLite `request_nonces` 表做 UNIQUE 防重放
  - 默认 `SIGNATURE_REQUIRED=true`，挂到 `/api/*` 子路由（`/health` 不签）
- **应用层加密**：AES-256-GCM。key = HKDF-SHA256(JWT_SECRET) 或 `ENCRYPTION_KEY_HEX`
  - 落库前加密 `messages.content` 与 `feedback.comment`，读时透明解密
- **签名 + 加密解耦**：前端 bundle 永远带签名（fallback 到 dev secret 让 `npm run dev` 默认能跑）；后端 `SIGNATURE_REQUIRED=false` 时 curl/Postman 不签也能 200（开发调试）

详细：见 [`backend/README.md`](backend/README.md)

---

## 目录结构

```
backend/                          # Go 后端
├── cmd/
│   ├── server/                   # 主 binary
│   ├── adminctl/                 # admin 账号管理 CLI（Argon2id PHC）
│   ├── demo-apply-refund/        # demo 用：不依赖 LLM 直接调 apply_refund
│   └── probe-minimax/            # connectivity probe（调试用）
├── internal/
│   ├── config/  apperr/  log/  middleware/  security/
│   ├── auth/    db/     repo/  seed/  model/
│   ├── agent/   ← Agent 循环（Runner + MemSink + trace）
│   ├── skill/   ← Skill 注册表 + 内置 + 动态加载 + 退款 ticket
│   ├── llm/     ← ChatCompleter + ToolChatCompleter + JSON fallback
│   ├── jev/     ← Jev HTTP 客户端
│   ├── service/ ← 流式 AgentChat + Admin + Feedback
│   ├── handler/ ← Chat (NDJSON) + SkillAdmin + RefundConfirm
│   ├── server/  ← router 装配
│   └── testutil/← 测试用 SQLite (tempdir)
├── data/
│   ├── *.db                       # SQLite 文件（gitignore）
│   └── skills/                    # hot-loaded skill JSON（demo 文件保留）
├── scripts/
│   ├── build.{sh,bat}             # 构建
│   ├── run.{sh,bat}               # 启动
│   ├── sign.js                    # HMAC 签名（curl 用）
│   ├── curl-signed.sh             # curl + 签名 wrapper
│   ├── eval.jsonl                 # 20 条评测用例
│   ├── eval.sh                    # 评测 runner（带签名）
│   ├── seed_demo_skills.sh        # SQL 直接 seed dynamic skill
│   └── demo.sh                    # 端到端 demo（不依赖 LLM）
├── certs/  *.pem                  # 自签证书（gitignore）
├── .env.example
└── README.md

frontend/                         # React SPA (Vite + TS + Tailwind)
├── src/
│   ├── App.tsx                    # 聊天界面 + 流式 TracePanel + 退款确认面板
│   ├── admin/
│   │   ├── AdminApp.tsx           # 后台路由
│   │   ├── ConversationListPage.tsx
│   │   ├── ConversationDetailPage.tsx  # 会话详情（含 skill_invocations）
│   │   ├── StatsPage.tsx
│   │   └── SkillsPage.tsx         # ← skill CRUD 后台页
│   ├── api.ts                     # axios + streamChat(NDJSON reader)
│   ├── security.ts                # HMAC 签名 (Web Crypto)
│   └── types.ts                   # StreamEvent / Skill / SkillMeta
├── public/
│   ├── _redirects                 # CF Pages SPA fallback + /api 转发
│   └── _headers                   # 安全头
└── vite.config.ts
```

---

## 测试

```bash
cd backend
go test -count=1 ./...
```

**14 个包 / 208 个 PASS 用例全绿**（新增 `tenant` / `industry` / `server`，`service` / `handler` 扩展）。其中：

- `internal/tenant`（12）：FromContext / WithTenant / Repo（EnsureDefault 幂等 + Delete 防删 tnt_default + FromHeader 校验）
- `internal/jev`（55）：Registry 双源加载 + Orchestrator 5 路径（live success / live fail→fallback / 模板缺失→fallback / client disabled / 超时）+ LocalRules 关键词匹配 + 3 类 Loopback + TemplateRepo（version 冲突）+ DecisionRepo（Stats P95 + MarkReviewed）
- `internal/llm`（24）：Router 3 通道（happy / 区域重排 / 全挂）+ CircuitBreaker（closed/open/half_open/cooldown）+ `RegionFromCtx` 公开
- `internal/industry`（10）：Loader FS / DB / 内置三层 + Activate 幂等 bump 版本
- `internal/handler`（20）：**v2.1 JevAdmin** 10 个端点 + **v2.1.1 LLMHealth endpoint**（JWT 鉴权 + 空 snapshot 容错）
- `internal/service`（19）：老 `Chat` 5 路径 + **`AgentChat` 8 路径**（v2.1.1 新增：Jev entry 持久化 / violate handover / clean 继续 / sensitive 不阻断 / JEV nil 兼容 / 关闭 BlockOnSensitive / 上游报错 fallback / Router failover）
- `internal/server` 🆕（5）：**v2.1.1** TenantGuard header 解析 + 404 unknown tenant / region hint 透传给 LLM Router / LLMHealth JWT 鉴权 + 空 snapshot / `/health` `/ready` 不走 TenantGuard
- `internal/skill`（20）：覆盖内置 skill（query_order / query_coupon / apply_refund）+ Tickets 状态机（happy path / 跨用户 / 重复 confirm / cancel-of-confirmed / SweepExpired）+ TicketSweeper 后台循环
- `internal/agent`（5）+ `internal/repo`（8）+ `internal/security`（17）：基础 runner / repo / 加密层

### 端到端 demo（不依赖 LLM key）

```bash
cd backend
bash scripts/demo.sh
```

全过 < 5 秒，验证五种端到端路径：

1. fs 热加载（`data/skills/*.json` → registry 启动时加载）
2. SQLite CRUD + 热加载（reload endpoint 让 in-memory 拿到）
3. `apply_refund` → pending_human ticket → confirm → `mock_orders.status='refunding'`
4. cancel ticket 路径
5. **后台 sweeper 自动 expired**（PRD REQ-007，覆盖 `TICKET_SWEEP_INTERVAL`）

### 管理员账号管理（不依赖 HTTP）

```bash
cd backend
./bin/adminctl -db ./data/intelligent_customer.db list              # 全部账号
./bin/adminctl -db ./data/intelligent_customer.db add ops           # 交互式（无回显输密码）
./bin/adminctl -db ./data/intelligent_customer.db add ops -p '...' # CI/容器 bootstrap
echo 'newpass' | ./bin/adminctl -db ./data/intelligent_customer.db passwd ops
./bin/adminctl -db ./data/intelligent_customer.db rename ops support
./bin/adminctl -db ./data/intelligent_customer.db del -y support
```

拒绝删除最后一个 admin（防止 dashboard 自锁）。详见 [`backend/README.md`](backend/README.md)。

### LLM 通道探测

```bash
cd backend
./bin/probe-minimax --key "$MiniMax_API_KEY" --url https://api.minimaxi.cn/anthropic/v1/messages
# MiniMax cn 证书异常时：--insecure
```

诊断 `MiniMax.cn` / OpenRouter 网络问题时第一手用。

---

## 上线检查清单

- [ ] `JWT_SECRET` 改为随机 ≥ 32 字节串
- [ ] `ADMIN_PASSWORD` 改为强密码（bootstrap 用），跑 `adminctl passwd admin`
- [ ] `LOG_LEVEL=info`、`LOG_PRETTY=false`
- [ ] `CORS_ORIGINS` 加上 Pages 域名（不要保留 `*`）
- [ ] `DB_PATH` 指向持久化卷
- [ ] `LLM_PROVIDER` + 对应 key 填好并验证可用（MiniMax cn 已可用，OpenAI / OpenRouter 也可）
- [ ] `AGENT_MAX_STEPS/MAX_TOKENS/MAX_WALLCLOCK` 按业务调整
- [ ] `TICKET_SWEEP_INTERVAL` 默认 1 分钟；如果产品场景 ticket TTL 更短可调小
- [ ] CF Pages 的 `_redirects` 把 `https://api.your-domain.com` 改成真实后端域名
- [ ] 后端走 HTTPS（前置 nginx / Caddy 或 Go 自带 TLS）
- [ ] 跑 `bash scripts/demo.sh` 确认 skill + 退款 + 后台 sweeper 闭环
- [ ] 跑 `bash scripts/eval.sh`（需真 LLM key）确认 20 条用例通过

---

## 配套文档（`docs/`）

代码 + 这个 README 不够装下全部设计。`docs/` 目录收齐 PRD、架构、路线图与决策记录（ADR）：

| 文件 | 内容 |
|---|---|
| [`docs/README.md`](docs/README.md) | docs/ 目录的索引页 |
| [`docs/PRD.md`](docs/PRD.md) | v1.0 产品需求（已交付能力 + 验收清单） |
| [`docs/architecture-overview.md`](docs/architecture-overview.md) | v2 目标架构（C4 + 5 条演进原则） |
| [`docs/architecture-roadmap.md`](docs/architecture-roadmap.md) | v2.1 → v2.3 时间线 + 风险矩阵 |
| [`docs/v2-roadmap.md`](docs/v2-roadmap.md) | v2 业务侧规划 + Jev 决策层 + 5 大完善方向 |
| [`docs/v2-jev-rollout.md`](docs/v2-jev-rollout.md) | **v2.1 Jev 决策层落地（5 类场景，已实现）** |
| [`docs/v2-jev-fallback.md`](docs/v2-jev-fallback.md) | **v2.1 Jev 降级策略（本地规则，已实现）** |
| [`docs/v2-jev-loopback.md`](docs/v2-jev-loopback.md) | **v2.1 Jev 效果回灌（混合方案，已实现）** |
| [`docs/v2-llm-routing.md`](docs/v2-llm-routing.md) | **v2 LLM 三通道主兜备（Router + CircuitBreaker，已实现）** |
| [`docs/v2-multitenancy.md`](docs/v2-multitenancy.md) | **v2.2 多租户架构（混合方案，v2.1 注入 tenant_id）** |
| [`docs/v2-rag.md`](docs/v2-rag.md) | v2.1 RAG 知识库设计（pgvector + BGE + Jev rerank；v2.1 推迟到 v2.2） |
| [`docs/v2-agent-workspace.md`](docs/v2-agent-workspace.md) | v2.1 坐席工作台设计（嵌入 admin；v2.1 推迟到 v2.1.1） |
| [`docs/v2-industry.md`](docs/v2-industry.md) | **v2.1 行业适配（通用版 general_v1 已实现）** |
| [`docs/v2-implementation-progress.md`](docs/v2-implementation-progress.md) | **v2.1 + v2.1.1 实现进度一页总览**（14 包 175+ 用例全绿；TenantGuard 装配 + Router 接 AgentChat + Orchestrator 触发点接入 chat pipeline） |
| [`docs/adr/`](docs/adr/) | 7 份 ADR（模块化单体 / 多租户 / 渠道适配 / RAG / LLM 路由 / Jev 决策层 / Jev 降级） |

新增能力时请同步在 ADR 增补一条；大的功能边界变更需要在 PRD "变更日志" 留痕。