# Frontend — React SPA

纯静态站点，可以直接拖到 Cloudflare Pages、Netlify、Vercel、或任何静态服务器。

> **配套**：[顶层 README](../README.md) · [后端 README](../backend/README.md) · [产品 PRD](../docs/PRD.md) · [docs/ 索引](../docs/README.md)
> **协议约定**：前端"始终带签名"（`src/security.ts` 拦截器自动注入 HMAC-SHA256），与后端 `SIGNATURE_REQUIRED` 开关正交；退款二次确认前端弹模态框，点确认调 `POST /api/skills/confirm {ticketId, userId}`。

## 技术栈

- React 18 + Vite 5 + TypeScript
- Tailwind CSS（darkMode:'class'）
- axios + React Router 6
- recharts（管理后台满意度图表）
- **react-markdown + remark-gfm + rehype-highlight + rehype-sanitize + highlight.js + dompurify**（v2.2 PR4：Markdown 渲染 + 代码高亮 + XSS sanitize）
- **Vitest + @testing-library/react + jsdom**（v2.2 PR3 起；前端单测框架）

## 目录

```
frontend/
├── package.json
├── vite.config.ts         # dev proxy /api → backend + manualChunks（v2.2 PR4）
├── vitest.config.ts       # v2.2 PR3：vitest + jsdom + setup
├── tsconfig.json
├── tailwind.config.js     # darkMode:'class' + 消费 design tokens
├── postcss.config.js
├── public/
│   ├── _redirects         # CF Pages SPA fallback + /api 代理（**生产部署前改域名**）
│   └── _headers           # 安全头
├── src/
│   ├── main.tsx           # 全局 ErrorBoundary 包裹
│   ├── App.tsx            # 聊天界面（PR3 + PR4：Markdown + 打字机 + 多会话 + 消息操作）
│   ├── admin/             # 管理后台（PR3：a11y + Skeleton + EmptyState + 响应式表格）
│   │   ├── AdminApp.tsx           # 暗色切换 + 移动端汉堡菜单
│   │   ├── ConversationListPage.tsx
│   │   ├── ConversationDetailPage.tsx
│   │   ├── StatsPage.tsx
│   │   ├── SkillsPage.tsx
│   │   ├── JevTemplatesPage.tsx
│   │   ├── JevObservabilityPage.tsx
│   │   ├── RolesPage.tsx          # v2.2 PR2 + PR3 a11y
│   │   ├── AuditLogPage.tsx       # v2.2 PR2 + PR3 a11y
│   │   └── RateLimitConfigPage.tsx # v2.2 PR2 + PR3 a11y
│   ├── components/        # 共享 UI 组件
│   │   ├── ErrorBoundary.tsx
│   │   ├── Skeleton.tsx           # v2.2 PR3：loading 骨架
│   │   ├── EmptyState.tsx         # v2.2 PR3：3 态空状态
│   │   ├── ThemeToggle.tsx        # v2.2 PR3：暗色切换按钮
│   │   ├── Markdown.tsx           # v2.2 PR4：Markdown 渲染 + rehype-sanitize + 代码块
│   │   ├── CodeBlock.tsx          # v2.2 PR4：代码块（语言标签 + 复制按钮 + a11y）
│   │   ├── ConversationList.tsx   # v2.2 PR4：多会话侧栏 / 列表
│   │   ├── Drawer.tsx             # v2.2 PR4：移动端抽屉
│   │   └── MessageActions.tsx     # v2.2 PR4：消息操作（复制 / 重试 / 👍 / 👎）
│   ├── design/            # v2.2 PR3：设计令牌
│   │   └── tokens.ts
│   ├── hooks/             # 自定义 hooks
│   │   ├── useTheme.ts            # v2.2 PR3：暗色 hook
│   │   ├── useTypewriter.ts       # v2.2 PR4：流式打字机 hook
│   │   └── useConversations.ts    # v2.2 PR4：多会话历史 hook（localStorage 50 条 FIFO）
│   ├── test/              # vitest setup
│   │   └── setup.ts
│   ├── api.ts             # axios 客户端 + Jev API (v2.1) + RBAC/Audit/RateLimit (v2.2 PR2)
│   ├── types.ts           # 含 JevTemplate / JevDecision / JevStats / RBAC / Audit / RateLimit
│   └── index.css          # Tailwind + 暗色 CSS 变量 + shimmer animation + focus ring + highlight.js GitHub theme + Markdown 容器样式
└── index.html             # pre-paint theme script + CSP meta
```

## Markdown 与 XSS 防护（v2.2 PR4）

所有 assistant 回复 / 任意 Markdown 内容统一走 `<Markdown />` 组件：

- **栈**：`react-markdown` → `remark-gfm`（表格 / 任务列表 / 删除线）→ `rehype-highlight`（代码高亮）→ `rehype-sanitize`（默认 schema，XSS 兜底）→ React 元素树。
- **链接**：自动 `target="_blank" rel="noopener noreferrer"`。
- **代码块**：右上角 📋 复制按钮 + 语言标签；aria-live 宣告。
- **XSS 5 用例必 100% 通过**（见 `Markdown.test.tsx`）：
  1. `<script>alert(1)</script>` → 剥离
  2. `<img src=x onerror=alert(1)>` → 剥离 onerror
  3. `[click](javascript:alert(1))` → 剥离 javascript: 协议
  4. `<iframe src=evil.com>` → 剥离
  5. `<svg onload=alert(1)>` → 剥离

## 多会话历史（v2.2 PR4）

- localStorage key=`ic.conversations`；最多 50 条 FIFO（PRD Q2 拍板）。
- 桌面端常驻侧栏（`hidden lg:block w-[280px]`）；移动端汉堡按钮 + Drawer 抽屉。
- 当前会话高亮；hover 显示删除按钮（confirm 二次确认）。
- localStorage 不可用时降级到内存（`degraded=true`）。
- 数据结构仅 `{role, content, timestamp}`（Q9 拍板，不含 metadata）。

## 消息操作（v2.2 PR4）

`MessageActions` 在 assistant 消息右下角（hover 显示）：

- **📋 复制**：把消息内容写入剪贴板；成功后 ✓ 1s。
- **🔄 重新生成**：删除最后一条 assistant 后，重新 send 最后一条 user 消息。
- **👍 / 👎**：调 `api.feedback(rating=5/2)`；激活态保持 aria-pressed。
- 受控 / 非受控 feedback 状态都支持。

## 流式打字机（v2.2 PR4）

`useTypewriter(text, { speedMs: 20 })`：

- 每 token 间隔 20ms（PRD §9.2 上限 30ms）。
- `skip()` 立即显示全文；`reset()` 清空。
- assistant 消息自动接入（`AssistantContent` 组件）。

## Bundle 拆分（v2.2 PR4）

`vite.config.ts` 配置 `manualChunks`：

- `react-vendor`：React / ReactDOM / React Router。
- `markdown`：Markdown 渲染栈（含 highlight.js ~341KB，独立缓存）。
- 主 entry：547.90 KB < 800KB 预算（gz 151.38 KB）。

## 开发

```bash
cd frontend
npm install
npm run dev               # http://localhost:5173，自动代理 /api → :8080
```

需要先启动 backend（参见 `backend/README.md`）。

## 测试

```bash
npm run test              # 单次跑（CI 模式）
npm run test:watch        # 监听模式（开发）
npm run test:coverage     # 带 coverage 报告
```

Vitest + jsdom + @testing-library/react。测试文件命名：`*.test.{ts,tsx}` / `*.spec.{ts,tsx}`，放在对应源文件同目录。

当前测试套件（v2.2 PR3，9 文件 / 66 用例）：

| 文件 | 用例 | 覆盖 |
|---|---|---|
| `design/tokens.test.ts` | 21 | 5 组 token 齐全 + 取值合规 |
| `hooks/useTheme.test.ts` | 7 | localStorage + matchMedia + 三态循环 |
| `components/ThemeToggle.test.tsx` | 7 | 渲染 + 点击切换 + localStorage 联动 |
| `components/Skeleton.test.tsx` | 10 | 3 形态 + count + a11y |
| `components/EmptyState.test.tsx` | 10 | 3 态 + 自定义 + a11y |
| `components/ErrorBoundary.test.tsx` | 5 | happy / error / fallback / scope / 唯一 ID |
| `admin/RolesPage.test.tsx` | 2 | 渲染 + mock 数据加载 |
| `admin/AuditLogPage.test.tsx` | 2 | 渲染 + 5 筛选字段 |
| `admin/RateLimitConfigPage.test.tsx` | 2 | 渲染 + 编辑入口 |

## 构建

```bash
npm run build             # 产物在 frontend/dist/（695KB / 200KB gz，< 800KB 预算）
```

`dist/` 是纯静态文件（index.html + assets/）。`tsc -b --noEmit` 在 CI 里跑一遍类型检查。

## 视觉系统（v2.2 PR3）

- **设计令牌**：`src/design/tokens.ts` 输出 5 组（colors / spacing / typography / radius / shadow），由 `tailwind.config.js` 通过 `theme.extend` 消费；CSS 变量 `bg-app-*` / `text-app` / `border-app-*` 提供亮 / 暗双套语义。
- **暗色模式**：`useTheme()` hook（localStorage `ic.theme`）+ `ThemeToggle` 组件（system / light / dark 三态）+ `index.html` pre-paint script 防 FOUC。
- **响应式**：用户端聊天页 mobile 单列 / tablet-desktop `max-w-3xl` 居中；管理后台 mobile 折叠侧栏 + 汉堡菜单、tablet-desktop 多列；表格 mobile 横滑（`.table-responsive`）。
- **Skeleton**：`<Skeleton variant="text|circle|rect" />`（应用 ≥ 6 处：聊天加载 / Stats / Conversations / Roles / Audit / RateLimit / Skills / JevTemplates / JevObservability）。
- **EmptyState**：`<EmptyState variant="noData|noResult|unauthorized" />`（应用 ≥ 4 处）。
- **a11y**：所有 button / a 加 `aria-label`（缺文本时）；input / textarea 配 `<label>`；统一 `focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-app-brand`；语义 HTML（`role="dialog"` / `aria-modal` / `aria-live`）。

## Cloudflare Pages 部署

1. **构建配置**（CF Pages 控制台 → Create → Direct Upload 或 Connect to Git）：

| 项 | 值 |
|---|---|
| Framework preset | None |
| Build command | `npm run build` |
| Build output directory | `dist` |
| Root directory | `frontend` |
| Environment variable | `VITE_API_BASE_URL=https://api.your-domain.com`（你的后端真实域名） |

2. **改 `public/_redirects`**：

```
/api/*  https://api.your-domain.com/api/:splat  200
/*      /index.html                            200
```

把 `https://api.your-domain.com` 换成你的后端域名。这样浏览器侧看到的 `/api/...` 全部走同源，CF Pages 在边缘帮你转发到后端，**前端代码不需要任何改动**。

3. **CORS**：后端 `.env` 的 `CORS_ORIGINS` 加上 Pages 域名：

```
CORS_ORIGINS=https://your-app.pages.dev,https://your-app.com
```

4. 直接 Upload：`frontend/dist/` 整个目录拖到 Pages 控制台。

## 配置

| 变量 | 何时设置 | 说明 |
|---|---|---|
| `VITE_API_BASE_URL` | 生产构建时 | 后端的绝对 URL（含 scheme，无尾斜杠）。留空 = 同源。 |
| `VITE_API_PROXY_TARGET` | 开发时 | vite dev 代理 `/api` 到哪里，默认 `http://localhost:8080`。 |
| `VITE_API_SIGN_SECRET` | 生产构建时 | 与后端 `JWT_SECRET` 同值的 HMAC 共享密钥。**前端始终自动带签名**，与后端 `SIGNATURE_REQUIRED` 开关正交——后端可临时关掉校验而不影响前端。 |

`.env.example` 默认填了 `dev-secret-please-change-in-production-must-be-32b`（与 `backend/.env.example` 的 `JWT_SECRET` 同值），所以 `cp .env.example .env && npm run dev` 默认就能跑通。

注意：`VITE_*` 会被打进前端 bundle，对用户可见——别放真正的生产密钥。签名密钥可放在 CF Pages 的环境变量里，构建时注入；只要 SPA 部署在可信域名（你的 pages.dev / 自定义域），中间人拿不到 headers 原文就重现不出 HMAC。

## 应用层安全

**前端始终加密通讯**：所有 `api.xxx` 调用都自动带 `X-Timestamp / X-Nonce / X-Signature` 头，无需手动处理。后端 `SIGNATURE_REQUIRED` 开关决定是否强制校验——开发期 `false`，curl 不签也能调；生产 `true`，未签 / 篡改 / 重放一律 401。

`src/security.ts` 用 Web Crypto API（`subtle.importKey` + `sign`）生成签名：

```ts
import { sign } from './security';

await sign('POST', url, JSON.stringify(body));
// → { 'X-Timestamp': '...', 'X-Nonce': '...', 'X-Signature': '...' }
```

`src/api.ts` 的 axios 拦截器已自动调用。

## v2.1 新页面

| 路径 | 组件 | 后端接口 |
|---|---|---|
| `/admin/jev/templates` | `JevTemplatesPage.tsx` | `GET/POST/DELETE /api/admin/jev/templates` + `/publish/:id` + `/archive/:id` + `/reload` |
| `/admin/jev/observability` | `JevObservabilityPage.tsx` | `GET /api/admin/jev/stats` + `GET /api/admin/jev/decisions` + `POST /api/admin/jev/decisions/:id/review` |

页面顶部展示 4 个核心指标卡（总决策数 / 降级率 / P95 延迟 / v2.1 目标），下面跟按模板分布表 + 决策日志表 + 模板筛选框 + 接受/拒绝按钮。

## v2.1.1 新接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/llm/health` | LLM Router 熔断器快照。返回每通道 `slot / provider / model / state`（`closed` / `open` / `half_open`），JWT 鉴权。当前前端还没有专属页面，可以接进 `/admin/jev/observability` 顶部作为运维卡片 |

`/api/chat` 服务端行为变化（前端无需改动）：
- `message.pre_ingest` 触发点返回 `violate` 时直接 200 + `{handedOver:true, handoverReason:"jev_violate"}`，前端照常显示"已转人工"提示
- `X-Tenant-ID` header 现在会被 TenantGuard 解析进 ctx；前端可选择性带上（默认 `tnt_default`）

详见 `backend/README.md` 的"v2.1.1 chat pipeline 与 Jev 触发点"小节。

详见 `backend/README.md` 的"Jev 决策层"小节。

## 后端配套

后端服务在 `../backend`，参见 `backend/README.md`。开发期两者同时跑：

```
backend  → :8080   （API）
frontend → :5173   （SPA，代理 /api → :8080）
```

部署期两者独立：

```
Cloudflare Pages → frontend/dist/         （静态 SPA）
任意容器 / 进程 → backend/bin/intelligent_customer   （API）
```
