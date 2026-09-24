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
├── cmd/server/main.go        # 入口（依赖注入 + graceful shutdown）
├── internal/
│   ├── config/               # .env 加载 + 校验 + case-insensitive provider
│   ├── apperr/               # 类型化错误（统一 JSON 输出）
│   ├── log/                  # zerolog + req_id + CallerHook
│   ├── middleware/           # RequestID · Recover · AccessLog · CORS · Auth
│   ├── auth/                 # JWT (HS256) 签发与校验
│   ├── db/                   # SQLite 连接 + embed 迁移
│   ├── model/                # 数据结构
│   ├── repo/                 # SQLite 仓储
│   ├── seed/                 # FAQ 种子（11 条）
│   ├── jev/                  # /alpha/decisions 客户端（choice / noul / score）
│   ├── llm/                  # OpenAI 兼容 + Anthropic 协议（MiniMax cn）
│   ├── service/              # 业务编排（Chat / Admin / Feedback）
│   ├── handler/              # HTTP controller
│   ├── server/               # chi 路由 + graceful shutdown
│   └── testutil/             # 测试用 OpenTempSQLite
├── scripts/{build,run}.{sh,bat}
├── certs/                    # 自签证书（不入库）
├── data/                     # SQLite 文件（不入库）
├── .env.example
└── go.mod                    # module intelligent_customer/backend
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
go test -run TestChatFAQHit      # 单用例
```

8 个包、30+ 用例：config / apperr / auth / jev（mock）/ llm（mock OpenAI + mock Anthropic）/ repo（真实 SQLite）/ service（真实 repo + stub LLM）/ handler（httptest + 真实链路）。

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

## 故障排查

| 现象 | 看日志关键字 | 排查 |
|---|---|---|
| 启动失败 `JWT_SECRET too short` | `jwt_secret_error` | 至少 16 字符 |
| LLM 502 | `llm_call_failed` | 检查 API Key、Base URL；常见证书过期（`SEC_E_CERT_EXPIRED`） |
| Jev 失败 | `jev_http_error` / `jev_transport_error` | 检查 `JEV_API_KEY` 与 `JEV_BASE_URL` |
| 前端连不上 | `http_request ... http=502` | CORS：浏览器 DevTools 看 preflight；检查 `CORS_ORIGINS` |
| 数据库锁 | `database is locked` | WAL 模式单写者，调高 `busy_timeout` 或减少并发 |