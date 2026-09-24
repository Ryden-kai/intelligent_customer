# Frontend — React SPA

纯静态站点，可以直接拖到 Cloudflare Pages、Netlify、Vercel、或任何静态服务器。

## 技术栈

- React 18 + Vite 5 + TypeScript
- Tailwind CSS
- axios + React Router 6
- recharts（管理后台满意度图表）

## 目录

```
frontend/
├── package.json
├── vite.config.ts         # dev proxy /api → backend
├── tsconfig.json
├── tailwind.config.js
├── postcss.config.js
├── public/
│   ├── _redirects         # CF Pages SPA fallback + /api 代理（**生产部署前改域名**）
│   └── _headers           # 安全头
├── src/
│   ├── main.tsx
│   ├── App.tsx            # 聊天界面
│   ├── admin/             # 管理后台
│   ├── api.ts             # axios 客户端；baseURL = VITE_API_BASE_URL
│   ├── types.ts
│   └── index.css
└── index.html
```

## 开发

```bash
cd frontend
npm install
npm run dev               # http://localhost:5173，自动代理 /api → :8080
```

需要先启动 backend（参见 `backend/README.md`）。

## 构建

```bash
npm run build             # 产物在 frontend/dist/
```

`dist/` 是纯静态文件（index.html + assets/）。

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