// Package middleware — security_headers.go
//
// v2.2 PR1：下发 7 个安全相关 HTTP 响应头。
//
// 7 个 header：
//   1. Content-Security-Policy           （详见 buildCSP）
//   2. X-Frame-Options                  DENY
//   3. X-Content-Type-Options           nosniff
//   4. Referrer-Policy                  strict-origin-when-cross-origin
//   5. Strict-Transport-Security        （仅生产：max-age=31536000; includeSubDomains）
//   6. Permissions-Policy               camera=(), microphone=(), geolocation=()
//   7. X-XSS-Protection                 0（现代浏览器默认 CSP 已足够）
//
// 触发方式：SecurityHeaders(isProd)。isProd 决定是否下发 HSTS。
// 调用链顺序（推荐）：RequestID → Recover → Sanitize → AccessLog → SecurityHeaders → ...
//
// 为什么 HSTS 仅生产：dev 多走 HTTP（无 TLS），HSTS 在 HTTP 下无效且浏览器会忽略；
// 但若统一下发会让浏览器缓存错误的"该域名强制 HTTPS"状态，反而干扰 dev 调试。
package middleware

import "net/http"

// SecurityHeaders 返回下发 7 个安全 header 的中间件。
//
// isProd = true 时下发 HSTS（前提是请求是 HTTPS，否则浏览器忽略）。
// 否则 HSTS 跳过，避免污染浏览器 HSTS 缓存。
func SecurityHeaders(isProd bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			// 1. CSP（最复杂，放到独立函数）
			h.Set("Content-Security-Policy", buildCSP())
			// 2. 防止 iframe 嵌入（clickjacking 防御）
			h.Set("X-Frame-Options", "DENY")
			// 3. 禁止 MIME 类型嗅探
			h.Set("X-Content-Type-Options", "nosniff")
			// 4. 限制 Referer 头泄露
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			// 5. HSTS（仅生产）
			if isProd {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			// 6. 关闭不必要的浏览器能力
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			// 7. 显式关闭 XSS auditor（现代浏览器 CSP 已足够，旧审计器有 bug）
			h.Set("X-XSS-Protection", "0")
			next.ServeHTTP(w, r)
		})
	}
}

// buildCSP 返回 Content-Security-Policy 字符串。
//
// 取值与 v2-ui-security-prd.md §6.5 + v2-architecture-design.md §3.8.1 一致：
//   - default-src 'self'
//   - script-src 'self' 'unsafe-inline' 'unsafe-eval'
//     → v2.2 接受 unsafe-inline（JIT 实测需要）+ unsafe-eval（部分 SDK 用到）。
//     → v2.2.1 评估 nonce 方案。
//   - style-src 'self' 'unsafe-inline'   （Tailwind JIT 必须）
//   - img-src 'self' data: https:         （允许 https 图与 base64）
//   - font-src 'self' data:
//   - connect-src 'self'                 （XHR / fetch 仅同源；SSE/WS 在网关层放宽）
//   - frame-ancestors 'none'             （替代 X-Frame-Options: DENY 的现代写法）
//   - base-uri 'self'
//   - form-action 'self'
func buildCSP() string {
	return "default-src 'self'; " +
		"script-src 'self' 'unsafe-inline' 'unsafe-eval'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: https:; " +
		"font-src 'self' data:; " +
		"connect-src 'self'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'"
}