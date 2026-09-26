# Backend — Go API 服务

只暴露 JSON API。`/api/*` 之外的所有路径都会返回 404 + JSON 提示（前端由 Cloudflare Pages 托管）。

## 快速开始

```bash
cd backend
cp .env.example .env       # 填入 JWT_SECRET、LLM/Jev key
go mod tidy                # 第一次需要
go run ./cmd/server        # 或 scripts/build.sh 后跑 bin/intelligent_customer
```

启动后访问：
- `http://localhost:8080/health` → `{"status":"ok"}`
- `http://localhost:8080/api/chat` (POST) → 聊天
- `http://localhost:8080/api/admin/login` (POST) → 拿 JWT

## 目录

```
backend/
├── cmd/
│   ├── server/                   # 主服务（intelligent_customer）
│   ├── adminctl/                 # admin 账号管理 CLI（Argon2id）
│   ├── demo-apply-refund/        # 绕过 LLM 直接调 apply_refund
│   └── probe-minimax/            # LLM 通道连通性诊断
├── internal/
│   ├── config/                   # .env 加载 + 校验 + case-insensitive provider
│   ├── apperr/                   # 类型化错误（统一 JSON 输出）
│   ├── log/                      # zerolog + req_id + CallerHook
│   ├── middleware/               # RequestID · Recover · AccessLog · CORS · Auth · Signature
│   ├── auth/                     # JWT (HS256) 签发与校验
│   ├── db/                       # SQLite 连接 + embed 迁移
│   ├── model/                    # 数据结构（含 Skill / SkillPendingTicket）
│   ├── repo/                     # SQLite 仓储（conversations / messages / admin_users / nonces / ...）
│   ├── seed/                     # FAQ 种子（11 条）+ admin bootstrap
│   ├── tenant/                   # v2.1 多租户 ctx + Repo + Middleware
│   ├── jev/                      # v2.1 决策层：Client + Registry + Orchestrator + LocalRules + Loopback + repos
│   ├── llm/                      # OpenAI 兼容 + Anthropic 协议 + v2.1 Router + CircuitBreaker
│   ├── industry/                 # v2.1 行业模板 loader（general_v1）
│   ├── agent/                    # Agent 循环（Runner + MemSink + trace_id 贯穿）
│   ├── skill/                    # 注册表 + 内置 + 动态加载 + ticket 状态机 + 后台 sweeper
│   ├── service/                  # 业务编排（AgentChat / Admin / Feedback）
│   ├── handler/                  # HTTP controller（Chat NDJSON / Admin / SkillAdmin / RefundConfirm / v2.1 JevAdmin）
│   ├── server/                   # chi 路由 + graceful shutdown
│   ├── security/                 # Argon2id 哈希 + HMAC 签名 + AES-256-GCM
│   └── testutil/                 # 测试用 OpenTempSQLite
├── templates/
│   └── jev/                      # v2.1 5 类 Jev YAML 模板（intent/sensitive/emotion/ticket_priority/agent_routing）
├── data/
│   ├── skills/                   # hot-loaded skill JSON
│   ├── jev_rules/                # v2.1 5 类本地规则 YAML（关键词降级）
│   ├── industry_templates/       # v2.1 行业模板 YAML（general_v1）
│   └── *.db                      # SQLite 文件（gitignore）
├── scripts/
│   ├── build.{sh,bat}            # 构建（出 intelligent_customer + adminctl）
│   ├── run.{sh,bat}              # 启动
│   ├── sign.js / curl-signed.sh  # curl 签名 helper
│   ├── eval.jsonl / eval.sh      # 20 条评测用例 + runner（需真 LLM key）
│   ├── demo.sh                   # 端到端 demo（不依赖 LLM；含 sweeper 验证）
│   └── seed_demo_skills.sh       # SQL 直接 seed dynamic skill（demo 用）
├── data/skills/                  # hot-loaded skill JSON（demo 文件保留）
├── certs/                        # 自签证书（不入库）
├── data/                         # SQLite 文件（不入库）
├── .env.example
└── go.mod                        # module intelligent_customer/backend
```

## 配置（.env）

| 变量 | 含义 | 默认 |
|---|---|---|
| `SERVER_ADDR` | 监听 | `:8080` |
| `LOG_LEVEL` | `debug`/`info`/`warn`/`error` | `debug`（开发） |
| `LOG_PRETTY` | 控制台带色输出 | `true` |
| `DB_PATH` | SQLite 文件 | `./data/intelligent_customer.db` |
| `JWT_SECRET` | 签名密钥 | 必填，≥ 16 字符 |
| `JWT_TTL_HOURS` | 有效期 | `24` |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD` | **仅用于 bootstrap** 首个后台账号 | `admin` / `admin123` |
| `LLM_PROVIDER` | `minimax` / `openai` | `minimax` |
| `MiniMax_BASE_URL` | MiniMax cn 端点（Anthropic 协议） | `https://api.minimaxi.cn/anthropic` |
| `MiniMax_MODEL` | MiniMax 模型 ID | `MiniMax-M3` |
| `MiniMax_API_KEY` | MiniMax 密钥 | 必填 |
| `OPENAI_BASE_URL` | OpenAI 通用端点 | `https://api.openai.com/v1` |
| `OPENAI_MODEL` | OpenAI 模型 | `gpt-4o-mini` |
| `OPENAI_API_KEY` | OpenAI / OpenRouter 密钥 | 必填（当 `LLM_PROVIDER=openai`） |
| `JEV_BASE_URL` | Jev 根端点 | 空=关闭 |
| `JEV_MODEL` | Jev 模型 | `typesafe/jev-1.13` |
| `JEV_API_KEY` | OpenRouter Key | 空=关闭 |
| `HANDOVER_CONFIDENCE_THRESHOLD` | 置信度低于此转人工 | `0.55` |
| `MSG_HANDOVER_COUNT_THRESHOLD` | 累计信号 ≥ 此值转人工 | `3` |
| `CORS_ORIGINS` | 允许的来源（逗号分隔） | `http://localhost:5173, ..., https://*.pages.dev` |
| `TLS_CERT_FILE` | HTTPS 证书 | 空=HTTP |
| `TLS_KEY_FILE` | HTTPS 私钥 | 空=HTTP |
| `AGENT_MAX_STEPS` | Agent 循环步数上限（任一触顶 → 转人工） | `5` |
| `AGENT_MAX_TOKENS` | 累计 token 预算 | `4000` |
| `AGENT_MAX_WALLCLOCK` | 单次 run 墙钟 | `20s` |
| `AGENT_SKILL_TIMEOUT` | 单个 skill 执行超时 | `5s` |
| `AGENT_SYSTEM_PROMPT` | 自定义 system prompt；留空用默认 | `""` |
| `TICKET_SWEEP_INTERVAL` | 后台 ticket 过期 sweeper 间隔；`0` 关闭 | `1m` |

## 构建 & 运行

```bash
# Linux / macOS
bash scripts/build.sh && bash scripts/run.sh

# Windows
scripts\build.bat && scripts\run.bat
```

构建产物：`backend/bin/intelligent_customer`（或 `.exe`）。

## HTTPS（TLS）

`.env` 同时设置 `TLS_CERT_FILE` 与 `TLS_KEY_FILE` 时，server 自动 `ListenAndServeTLS`；否则走 HTTP + WARN。开发用自签证书：

```bash
cd backend
mkdir -p certs
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout certs/localhost-key.pem -out certs/localhost.pem -days 365 \
  -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1,IP:::1"
```

`.env`：
```
TLS_CERT_FILE=./certs/localhost.pem
TLS_KEY_FILE=./certs/localhost-key.pem
```

## 测试

```bash
cd backend
go test ./...                   # 全部
go test -v ./internal/service   # 详细
go test -run TestAgentChat      # v2.1.1 chat pipeline 集成
go test -run TestTenantGuard    # v2.1.1 TenantGuard
```

**14 个包、208 个 PASS 用例全绿**（`go test -v` 统计；含子用例）。

| 包 | 覆盖范围 |
|---|---|
| `config` | env 加载 + provider 归一化 + JWT_SECRET 长度校验 |
| `apperr` | 类型化错误 + WithCause / WithDetail |
| `auth` | JWT 签发 + 解析 + TTL |
| `security` | Argon2id PHC round-trip + HMAC 签名 + AES-GCM 加解密 |
| `db` | SQLite open + 迁移 |
| `repo` | 真实 SQLite：conversations / messages（加解密）/ feedback / faqs / handover_signals / nonces / admin_users（CRUD + ErrNotFound） |
| `tenant` | **v2.1 多租户 ctx + Repo（EnsureDefault / Upsert / List / Delete 防删 tnt_default）+ Middleware + FromHeader 校验** |
| `jev` | **v2.1 决策层全栈：Registry（FS YAML + DB 双源）+ Orchestrator（live / fallback / 决策日志 / loopback / 5 类模板）+ LocalRules（关键词 + regex + score）+ Loopback（InMemory / DB / Satisfaction）+ TemplateRepo + DecisionRepo（Stats / MarkReviewed / version conflict）+ Decision() 通用入口 + 6 触发点** |
| `llm` | mock OpenAI + mock Anthropic + tool-call 协议 + JSON fallback + **v2.1 Router（3 通道 + 区域重排 + 失败降级 + 熔断）+ CircuitBreaker（closed/open/half_open + cooldown + StateChange hook）+ RegionFromCtx 公开（v2.1.1）** |
| `industry` | **v2.1 行业模板 loader（FS → DB → 内置 general_v1）+ Activate（materialise 3 个 jev_template 行，幂等 bump version）** |
| `service` | 真实 repo + stub LLM：chat 路由、handover、admin、feedback + **v2.1.1 AgentChat 8 路径**（Jev entry 持久化 / violate handover / clean 继续 / sensitive 不阻断 / JEV nil 兼容 / 关闭 BlockOnSensitive / 上游报错 fallback / Router failover） |
| `agent` | Runner 循环：max_steps / max_tokens / max_wallclock / llm_error / pending_human 终止 |
| `skill` | **内置 skill（query_order / query_coupon / apply_refund）+ Tickets 状态机（happy / 跨用户 / 重复 / cancel-of-confirmed / SweepExpired）+ TicketSweeper 后台循环** |
| `handler` | Chat / Admin / Feedback / **RefundConfirm**（confirm 跨用户 403、cancel-of-confirmed 404、缺字段 400、bad JSON 400）+ **v2.1 JevAdmin（list/create/get/publish/archive/delete/reload + decisions/stats/review + JWT 强制）+ v2.1.1 LLMHealth（GET /api/admin/llm/health 返回 Router CircuitBreaker snapshot）** |
| `server` 🆕 | **v2.1.1** TenantGuard header 解析 + 404 unknown tenant / region hint 透传给 LLM Router / LLMHealth JWT 鉴权 + 空 snapshot 容错 / `/health` `/ready` 不走 TenantGuard |

## 上线检查

- [ ] `JWT_SECRET` 改为随机 ≥ 32 字节（同时也是 HMAC 签名 + AES key 派生来源）
- [ ] `ADMIN_PASSWORD` 改强密码（首次启动后用 `adminctl passwd admin` 改）
- [ ] `LOG_LEVEL=info`、`LOG_PRETTY=false`
- [ ] `CORS_ORIGINS` 加上 Pages 域名
- [ ] `DB_PATH` 指向持久化卷
- [ ] 真实 LLM / Jev key
- [ ] 前端 `VITE_API_SIGN_SECRET` 与 `JWT_SECRET` 同值（生产）

## 后台账号（admin）

后台账号存 SQLite 表 `admin_users`，密码用 **Argon2id** 哈希（PHC 字符串格式）。env 里的 `ADMIN_USERNAME` / `ADMIN_PASSWORD` **只用于在 admin_users 为空时 bootstrap 第一个管理员**——之后所有账号都通过 `adminctl` 管理，env 值可以清空。

参数（`internal/security/password.go`）：
- Argon2id, time=2, memory=64 MiB, threads=4, salt=16 B, key=32 B

### adminctl CLI

`adminctl` 直接打开 SQLite（不需要 server 在跑），路径支持 `-db` 或 `$DB_PATH`：

```bash
# 列出现有账号
go run ./cmd/adminctl -db ./data/intelligent_customer.db list

# 新增账号（密码从 stdin 读，无回显）
adminctl add ops
# 或通过 -p（会警告"密码出现在进程表里"）
adminctl add ops -p "temporary-password"

# 改密码（CI/容器 bootstrap 用 stdin pipe）
echo 'new-pass-1234' | adminctl passwd ops

# 改名
adminctl rename ops support

# 删除（拒绝删最后一个管理员；-y 跳过确认）
adminctl del -y support
```

子命令：`list` `add <username>` `passwd <username|id>` `rename <username|id> <new>` `del <username|id>`。`<username|id>` 接受用户名或 UUID。删除最后一个 admin 会被拒绝（保护 dashboard 不会自锁）。

### 第一个管理员怎么来

`seed.Run` 检测 `admin_users` 为空且 bootstrap 字段非空 → 用 Argon2id 哈希密码后插入一行。日志里会有：

```
seed_admin_created_bootstrap_change_password_via_adminctl
```

之后立刻用 `adminctl passwd admin` 换一个强密码（或直接删掉 `.env` 里的 `ADMIN_PASSWORD`）。

## CLI 工具一览

后端除主服务外还有 3 个辅助 CLI（`cmd/*/main.go`），都不依赖 server 进程：

| CLI | 用途 | 用法 |
|---|---|---|
| `intelligent_customer` | 主服务 | `./bin/intelligent_customer`（已部署） |
| `adminctl` | 管理员账号管理 | 见上方"后台账号"小节 |
| `demo-apply-refund` | **绕过 LLM** 直接调 `apply_refund` skill | `demo-apply-refund --db <path> --user demo-user --order ORD-1001 --amount 5000 --reason "..."` → 输出 `{ticketId, status, summary}` JSON |
| `probe-minimax` | 探测 LLM 通道连通性 | `probe-minimax --key $MiniMax_API_KEY --url https://api.minimaxi.cn/anthropic/v1/messages`（证书异常时加 `--insecure`） |

### demo-apply-refund（不依赖 LLM）

PRD REQ-017：CI / 新人 onboarding 必须能不靠真 key 跑通退款闭环。

```bash
demo-apply-refund --db ./data/intelligent_customer.db \
                  --user demo-user \
                  --order ORD-1001 \
                  --amount 5000 \
                  --reason "演示退款"
# stdout:
# {
#   "ticketId": "<uuid>",
#   "summary": "已生成待确认退款工单 <uuid>",
#   "status": "pending_human",
#   "amount_cents": 5000
# }
```

必填：`--db`、`--user`、`--order`、`--amount > 0`。缺少任意一个 → exit 2（stderr 提示）。
后续用 `POST /api/skills/confirm {ticketId, userId}` 完成 confirm；用 `POST /api/skills/cancel {ticketId, userId}` 取消。

`scripts/demo.sh` 是端到端 wrapper：起 server → 调 demo-apply-refund → 调 confirm/cancel → 验证 DB 副作用 + **后台 sweeper 自动 expired**（覆盖 PRD REQ-007）。

### probe-minimax（连通性诊断）

碰到 `llm_call_failed` 时的第一手工具。直接发一个最小可用请求给 MiniMax cn 的 Anthropic 端点，打印：

- HTTP 状态码 + 耗时
- 响应体（前 4 KB）
- TLS 证书 subject / issuer / NotAfter（用于排查 `SEC_E_CERT_EXPIRED`）

```bash
probe-minimax --key "$MiniMax_API_KEY"
# 默认 URL: https://api.minimaxi.cn/anthropic/v1/messages

probe-minimax --key "$MiniMax_API_KEY" --insecure
# MiniMax 平台证书异常时绕过 TLS 验证（仅调试）
```

正常输出：

```
status=200 in 482ms
{"id":"...","type":"message","content":[{"type":"text","text":"你好"}]}
cert[0] subject=CN=*.minimaxi.cn issuer=CN=... expiry=2026-12-31T23:59:59+08:00
```

## 后台定时任务

| 任务 | 实现 | 间隔 | 关闭方式 |
|---|---|---|---|
| ticket 过期清理 | `internal/skill/ticket_sweeper.go`：`Tickets.SweepExpired` 把 `status='pending'` 且 `expires_at < now` 的 ticket 翻成 `expired` | 默认 1 分钟（`TICKET_SWEEP_INTERVAL`） | `TICKET_SWEEP_INTERVAL=0`（启动日志 `ticket_sweeper_disabled_set_TICKET_SWEEP_INTERVAL` 警告） |

PRD REQ-007："未确认 ticket 必须 5 分钟内自动 expired"。Sweeper 是单 ticker goroutine，graceful shutdown 走 `ctxRun`（SIGINT/SIGTERM 取消）。

`scripts/demo.sh` §8 用 `TICKET_SWEEP_INTERVAL=500ms` 起服务并注入过期 ticket，验证 1.5 秒内 status 翻成 `expired`。

## 应用层安全

### 接口签名（HMAC-SHA256）

所有 `/api/*` 请求必须带 3 个 header：

| Header | 值 |
|---|---|
| `X-Timestamp` | 客户端 unix 毫秒（偏差 ±5 分钟） |
| `X-Nonce` | 16 字节随机 hex（共 32 字符） |
| `X-Signature` | `hex(HMAC-SHA256(secret, canonical))` |

`canonical` 拼装：

```
<METHOD>\n<PATH_WITH_QUERY>\n<TIMESTAMP>\n<NONCE>\n<sha256-hex(body)>
```

`secret` 默认等于 `JWT_SECRET`。**前端始终自动带签名**（见 `frontend/src/api.ts` 的 axios 拦截器），不需手动处理。

**后端严格度由 `SIGNATURE_REQUIRED` 开关控制**：
- `true`（默认，生产保持）：无签名 / 错签名 / 重放 → 401
- `false`（开发调试）：curl / Postman 不签也能调，方便 ad-hoc 测试

**防重放**：服务端的 `request_nonces` 表用 UNIQUE 约束做原子"是否见过"判定；命中即返回 `401 nonce reused (replay detected)`。

### 应用层加密（AES-256-GCM）

落库前加密 `messages.content` 和 `feedback.comment`：

```
key = HKDF-SHA256(JWT_SECRET, salt=nil, info="intelligent_customer/app-encryption/v1")
每条密文 = base64( random12 || ciphertext || gcm16 )
```

DB 里看到的 `content` 是 base64 密文（不可读）。读取时自动解密，handler 输出仍是明文。

也可以通过 `ENCRYPTION_KEY_HEX=...`（64 hex 字符 = 32 字节）独立指定加密 key，便于与 JWT 解耦、独立轮换。

**开关**：`ENCRYPT_SENSITIVE_FIELDS=true`（默认）开启；`false` 关掉（迁移期或调试）。

### curl 测试

```bash
# 用 backend/scripts/curl-signed.sh 一把生成签名头并发起请求
bash scripts/curl-signed.sh POST /api/chat '{"userId":"u","content":"怎么申请退款？"}'
```

`backend/scripts/sign.js` 是纯 Node 工具，从 `.env` 读 `JWT_SECRET`，输出 header + body 供 curl 使用。

### 退款二次确认 API（不依赖 JWT）

`POST /api/skills/confirm` 和 `POST /api/skills/cancel` 是**公共端点**（不走 JWT），靠 `ticketId + userId` 配对鉴权——确保当前用户不能 confirm 别人的 ticket。

| 方法 | 路径 | 请求体 | 成功响应 | 失败码 |
|---|---|---|---|---|
| POST | `/api/skills/confirm` | `{"ticketId":"<uuid>","userId":"<id>"}` | 200 + ticket JSON `{"status":"confirmed","confirmedAt":...}` | 400 缺字段 / 403 跨用户或 ticket 不存在 / 409 已 confirmed/cancelled/expired |
| POST | `/api/skills/cancel` | 同上 | 204 No Content | 400 / 403 / 404（已 confirmed/cancelled 的 ticket 不再允许 cancel） |

内部流程（`Tickets.Confirm`）：
1. 事务 + 行锁读取 `skill_pending_tickets`（防并发 confirm）
2. 校验 `user_id` 一致 + `status='pending'` + `expires_at > now`
3. 执行 mutation（`apply_refund` → `UPDATE mock_orders SET status='refunding'`）
4. 更新 `ticket.status='confirmed'` + `confirmed_at=now`
5. Commit

过期 ticket 即使 sweeper 还没跑也会在 confirm/cancel 时被原地翻成 `expired`，行为与 sweeper 一致。

## v2.1 多租户

v2.1 把 `tenant_id` 注入到所有业务表（migration 005），并新增 `tenants` / `tenant_users` 两张元数据表。`internal/tenant` 包提供：

- `tenant.FromContext(ctx)` — 提取 ctx 中的 tenant，没有就返回 `tnt_default`
- `tenant.MustFromContext(ctx)` — 严格版，没有就返回 `*tenant.MissingError`
- `tenant.WithTenant(ctx, info)` — 把 tenant 注入 ctx
- `tenant.HeaderName = "X-Tenant-ID"` — HTTP header；中间件自动读
- `tenant.Repo` — CRUD（EnsureDefault / Upsert / List / Delete 防删默认租户）
- `tenant.Middleware(repo, issuer)` — **v2.1.1** 全局装配到 `/api` 子路由组（`server.go`）

启动日志会输出 `tenant_default_ready`，说明 migration 006 已经种好 `tnt_default` 行。`/health` `/ready` 故意不走 TenantGuard，避免 k8s liveness 探针在 tenants 表未就绪时挂掉。v2.2 会在此基础上做 RLS + RBAC（ADR-002）。

## v2.1 LLM 三通道

`internal/llm/router.go` 实现 `LLMRouter`，封装 `primary / secondary / tertiary` 三个 `Channel`，每个通道配一个 `CircuitBreaker`：

- `Router.Chat(ctx, sys, msgs)` 与 `Router.ToolChat(ctx, sys, msgs, tools)` 兼容 `ChatCompleter` / `ToolChatCompleter` 接口
- 区域重排：通过 `llm.WithRegionHint(ctx, "cn")` 让 router 把匹配该区域的通道放到前面（**v2.1.1 由 TenantGuard 自动注入**）
- 熔断器：连续 3 次失败 → open → 30s cooldown → half_open probe → 成功 → closed；状态变化有 `OnStateChange` hook
- 失败链：`primary` 失败 → `secondary` → `tertiary`；任一通道可加 `Regions: []string{"cn"}` 标记只在对应租户首选

**v2.1.1 已接入 AgentChat**：`buildLLMRouter(cfg, logger)` 根据 env 构造 1-3 通道 Router；用户配置 MiniMax + OpenAI 双 key 时自动出 2 通道，单 provider 时回退到 legacy 单 channel（仍包一层 Router snapshot 给 health 端点）。`cmd/server/main.go` 的 `LLM:` 字段直接拿 Router。

`GET /api/admin/llm/health`（JWT 鉴权）返回 `ChannelStates()` 快照：`{"channels":[{"slot":"primary","provider":"openai","model":"gpt-4o-mini","state":"closed"},...]}`。Router 已实现 `RegisterCallback(slot, provider, model, latency, err)` 用于把每次调用落到 `llm_call_log` 表。

## v2.1 Jev 决策层

`internal/jev/` 5 个核心组件：

| 文件 | 职责 |
|---|---|
| `registry.go` | 模板注册表；`LoadFromFS(dir)` + `LoadFromDB(conn)`；优先级 DB > FS > 内置 5 个默认模板 |
| `orchestrator.go` | `Orchestrator.Decide(ctx, req)` 编排：resolve template → live call → fallback → 决策日志 → loopback |
| `fallback.go` | `LocalRules` 关键词匹配 + `LocalRuleFallback` 兜底实现；6 套本地规则在 `backend/data/jev_rules/*.yaml` |
| `loopback.go` | `InMemoryLoopback` + `DBLoopback` 占位 + `SatisfactionLoopback`（feedback 满意度反哺） |
| `repo.go` | `TemplateRepo`（CRUD + Publish/Archive + version 冲突）+ `DecisionRepo`（List/Stats/MarkReviewed） |

模板与本地规则分别由 `backend/templates/jev/*.yaml` 和 `backend/data/jev_rules/*.yaml` 提供，YAML 字段：name / version / trigger / output_type / labels / instructions / fallback。orchestrator 用 `{{.key}}` 模板语法替换 Jev `/v1/state` 字段。

行业模板由 `internal/industry` 包管理。启动时 `industry.Activate("general_v1")` 把通用电商的 intent/sensitive/ticket_priority 三类规则 materialise 成 `tnt_default` 的 `jev_templates` 行，幂等 bump 版本。

### 触发点矩阵

| 触发点 | 模板 | 输出 | v2.1.1 chat pipeline 行为 |
|---|---|---|---|
| `message.entry` | `intent_routing` | choice（order/refund/coupon/faq/chitchat/sensitive/complaint/out_of_scope） | **已接**：informational 写入 `jev_decisions`，不改变路由 |
| `message.pre_ingest` | `sensitive_check` | choice（clean/sensitive/violate） | **已接**：violate → 立即 block + handover；sensitive → 放行但 audit |
| `reply.pre_ingest` | `sensitive_check` | choice | **已接**：informational + audit row |
| `handover.pre` | `emotion_detection` | score（angry/anxious/neutral/satisfied/urgent） | 待 v2.2 接坐席工作台 |
| `ticket.create` | `ticket_priority` | choice（p0/p1/p2/p3） | 待 v2.1.1.1 接工单系统 |
| `agent.assign` | `agent_routing` | choice（default_skill_group/vip_skill_group/tech_skill_group） | 待 v2.2 接坐席分配 |

Jev 通道失败 → fallback（本地规则关键词 P95 ≤ 10ms）；模板缺失 → fallback 走默认 label；fallback 也失败 → 上抛 apperr.Upstream。

### Admin 接口（v2.1 新增）

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET /api/admin/jev/templates` | 列出当前租户的 jev_templates 行 |
| `GET /api/admin/jev/templates/{id}` | 查单条 |
| `POST /api/admin/jev/templates` | 新建模板（默认 status=draft） |
| `POST /api/admin/jev/templates/publish/{id}` | 翻 published + 自动 reload registry |
| `POST /api/admin/jev/templates/archive/{id}` | 翻 archived + 自动 reload |
| `DELETE /api/admin/jev/templates/{id}` | 删除 + 自动 reload |
| `POST /api/admin/jev/templates/reload` | 重新扫 FS + 重载 DB |
| `GET /api/admin/jev/decisions` | 决策日志（分页 + template / trigger / status 筛选） |
| `GET /api/admin/jev/decisions/{id}` | 单条决策详情 |
| `POST /api/admin/jev/decisions/{id}/review` | 接受 / 拒绝（人工标注） |
| `GET /api/admin/jev/stats` | 7 天窗口的 stats（total / fallbackRate / P95 / byTemplate） |

全部走 JWT；reload 触发后下次请求立即生效。

## 故障排查

| 现象 | 看日志关键字 | 排查 |
|---|---|---|
| 启动失败 `JWT_SECRET too short` | `jwt_secret_error` | 至少 16 字符 |
| LLM 502 | `llm_call_failed` | 检查 API Key、Base URL；常见证书过期（`SEC_E_CERT_EXPIRED`） |
| Jev 失败 | `jev_http_error` / `jev_transport_error` / `jev_live_call_failed_using_fallback` | 检查 `JEV_API_KEY` 与 `JEV_BASE_URL`；fallback 走了本地规则 |
| LLM 路由断路 | `llm_router_circuit_state_change` | 某通道连续失败 ≥ 3 → open，等 30s cooldown → half_open probe；看 `GET /api/admin/llm/health` 实时状态 |
| 前端连不上 | `http_request ... http=502` | CORS：浏览器 DevTools 看 preflight；检查 `CORS_ORIGINS` |
| 数据库锁 | `database is locked` | WAL 模式单写者，调高 `busy_timeout` 或减少并发 |
| Industry 激活失败 | `industry_activate_failed` | 检查 `tnt_default` 是否被外部 DELETE；或模板 YAML 校验失败 |
| TenantGuard 404 | `tenant not found` | 检查 `X-Tenant-ID` header 值是否在 `tenants` 表里；匿名请求会自动回退 `tnt_default` |
| chat pipeline 没有 Jev 决策 | （无日志） | 检查 `agentChatSvc.JEV` 是否非 nil（cmd/server/main.go 默认 wire） |

### v2.1.1 chat pipeline 与 Jev 触发点

`service.AgentChat` 在每条 chat 请求里按顺序触发：

1. **`fireJevEntry`** — `message.entry` 触发点，informational；只写 `jev_decisions`，不影响 routing。
2. **`fireJevPreIngest`** — `message.pre_ingest` 触发点；返回 `violate` 时 block + 立即 handover（`HandedOver=true`、`HandoverReason="jev_violate"`、assistant 写一段"已转人工复核"的话术）。可通过 `BlockOnSensitive=false` 关掉（仍 audit）。
3. **`fireJevPreReply`** — `reply.pre_ingest` 触发点；informational + audit，**不修改**已落库的 assistant 消息。

JEV 上游报错 / 超时走 `Orchestrator.Decide` 的 fallback 路径（本地关键词 + 默认 label），不会 crash chat；测试见 `internal/service/agent_chat_test.go`。