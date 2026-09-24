# 智能客服 Agent

一个支持多轮对话、FAQ 检索、Jev 意图识别、自动转人工、对话历史持久化、后台管理 + 满意度统计的全栈客服系统。

本仓库是 **两个独立的可分别部署的工程**：

```
.
├── backend/         # Go API 服务（chi + SQLite + Jev + LLM）
├── frontend/        # 纯静态 SPA（React + Vite + TS），适合 Cloudflare Pages
├── README.md        # ← 你在这里
├── .gitignore
└── .workbuddy/      # 本地工作记忆（不入业务）
```

- 后端是单一 Go binary，只暴露 `/api/*` JSON 接口。
- 前端 build 出 `frontend/dist/`，是纯静态资源，可以直接拖到 Cloudflare Pages / 任何静态托管。
- 前后端通过 CORS 跨域连接。`backend/.env` 的 `CORS_ORIGINS` 加上前端域名即可。

## 功能

- 多轮对话（最近 12 条历史 + LLM system prompt 注入）
- FAQ 知识库：内置 11 条种子（退款/订单/技术/通用），意图匹配 + 关键词打分
- 意图识别：Jev（OpenAI 兼容 `/alpha/decisions`，`choice`），失败回落到 LLM 提示词
- 转人工：Jev `noul` + 关键词 + 置信度兜底 + 累计信号次数阈值
- 对话历史 + 满意度：SQLite (WAL)
- 管理后台：登录 / 会话列表 / 详情 / 人工回复 / 满意度图表（recharts）
- 全链路 DEBUG 日志：开发期 `LOG_LEVEL=debug` + 控制台带颜色，每请求一个 req_id 贯穿 HTTP → service → Jev/LLM
- **应用层安全**：
  - 接口签名：HMAC-SHA256 + 防重放 nonce + ±5 分钟时间窗，所有 `/api/*` 强制
  - 应用层加密：AES-256-GCM，敏感字段（messages.content、feedback.comment）落库前加密、读时解密
  - TLS（传输层）：可选用前置 nginx / Caddy 终止，或直接 `ListenAndServeTLS`

## 快速开始（开发）

```bash
# 终端 A：后端（监听 :8080）
cd backend
cp .env.example .env        # 填入 JWT_SECRET、LLM/Jev key
bash scripts/build.sh       # 或 scripts\build.bat
bash scripts/run.sh         # → http://localhost:8080/health

# 终端 B：前端（监听 :5173，自动代理 /api → :8080）
cd frontend
npm install
npm run dev                 # → http://localhost:5173
```

浏览器打开 `http://localhost:5173`：
- 根路径 `/` 是聊天界面
- `/admin` 是管理后台（默认 `admin/admin123`，改 `.env` 里的 `ADMIN_USERNAME` / `ADMIN_PASSWORD`）

## 部署

### 前端 → Cloudflare Pages

```bash
cd frontend
npm install
npm run build       # 产物在 frontend/dist/
```

在 Cloudflare Pages 控制台：

| 项 | 值 |
|---|---|
| Framework preset | **None**（或 Vite） |
| Build command | `npm run build` |
| Build output directory | `dist` |
| Root directory | `frontend` |
| Environment variables | `VITE_API_BASE_URL=https://api.your-domain.com`（你的后端域名） |

`frontend/public/_redirects` 已经写好 SPA fallback（`/* → /index.html`）以及 `/api/*` 转发到后端；`frontend/public/_headers` 设了基础安全头。**但生产环境的 `/api/*` 转发需要替换 `_redirects` 里的 `https://api.your-domain.com` 为你的真实后端域名。**

### 后端 → 任意容器 / 进程

```bash
cd backend
go build -trimpath -o bin/intelligent_customer ./cmd/server
```

把 `bin/intelligent_customer` + 同目录的 `.env` 拷到服务器跑就行。也可以用 Docker / 直接 systemd 托管。

`.env` 关键项：
- `CORS_ORIGINS`：加上你的 Pages 域名，如 `https://your-app.pages.dev`
- `JWT_SECRET`：≥ 32 字节随机串
- `ADMIN_PASSWORD`：上线前改
- `LLM_PROVIDER` + 对应 key
- `JEV_API_KEY`（可选）
- `TLS_CERT_FILE` / `TLS_KEY_FILE`（可选；不想自己终止 TLS 就由前置 nginx / Caddy 终止）

## 目录结构

```
backend/                          # Go 后端
├── go.mod                        # module intelligent_customer/backend
├── cmd/server/main.go
├── internal/
│   ├── config/  apperr/  log/  middleware/
│   ├── auth/    db/     repo/  seed/
│   ├── model/   service/  handler/  server/
│   ├── jev/     llm/     testutil/
├── scripts/{build,run}.{sh,bat}
├── certs/                        # 自签证书（不入库）
├── data/                         # SQLite 文件（不入库）
├── .env.example
└── README.md

frontend/                         # React SPA（Vite + TS + Tailwind）
├── package.json
├── vite.config.ts
├── tsconfig.json
├── public/
│   ├── _redirects               # CF Pages SPA fallback
│   └── _headers                 # CF Pages 安全头
├── src/
│   ├── App.tsx                  # 聊天界面
│   ├── admin/                   # 管理后台
│   ├── api.ts                   # axios 客户端，baseURL 走 VITE_API_BASE_URL
│   ├── types.ts
│   ├── main.tsx
│   └── index.css
└── README.md
```

详细文档：
- `backend/README.md` — API 服务说明（接口、配置、测试、HTTPS）
- `frontend/README.md` — 前端独立构建 / CF Pages 部署 / 本地开发

## 测试

```bash
cd backend && go test ./...
```

覆盖：config / apperr / auth / jev（httptest mock）/ llm OpenAI 客户端（mock）/ Anthropic 客户端（mock）/ repo（真实 SQLite）/ service（真实 repo + stub LLM）/ handler（httptest + 真实链路）。

## 接口摘要

公开：

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/chat` | 发消息，返回 intent / 置信度 / 转人工 / source |
| POST | `/api/feedback` | 满意度（1-5 + 评论） |

后台（JWT）：

| 方法 | 路径 |
|---|---|
| POST | `/api/admin/login` |
| GET | `/api/admin/me` |
| GET | `/api/admin/conversations` |
| GET | `/api/admin/conversations/{id}` |
| POST | `/api/admin/conversations/{id}/reply` |
| GET | `/api/admin/stats/satisfaction` |

## 上线检查清单

- [ ] `JWT_SECRET` 改为随机 ≥ 32 字节串
- [ ] `ADMIN_PASSWORD` 改为强密码
- [ ] `LOG_LEVEL=info`、`LOG_PRETTY=false`
- [ ] `CORS_ORIGINS` 加上 Pages 域名（不要保留 `*`）
- [ ] `DB_PATH` 指向持久化卷
- [ ] LLM / Jev key 填好并验证可用
- [ ] CF Pages 的 `_redirects` 把 `https://api.your-domain.com` 改成真实后端域名
- [ ] 后端走 HTTPS（前置 nginx / Caddy 或 Go 自带 TLS）