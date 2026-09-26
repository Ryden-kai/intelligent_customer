package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// echoHandler 把 headers 转成 JSON 输出，方便测试断言。
var echoHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
})

func runSecurityHeaders(t *testing.T, isProd bool) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	SecurityHeaders(isProd)(echoHandler).ServeHTTP(w, r)
	return w
}

// TestSecurityHeaders_All7HeadersProd 验证生产环境 7 个 header 全部下发。
func TestSecurityHeaders_All7HeadersProd(t *testing.T) {
	w := runSecurityHeaders(t, true)
	h := w.Header()

	// 1. CSP
	csp := h.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP missing default-src: %q", csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP missing frame-ancestors: %q", csp)
	}

	// 2. X-Frame-Options
	if got := h.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	// 3. X-Content-Type-Options
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	// 4. Referrer-Policy
	if got := h.Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy = %q, want strict-origin-when-cross-origin", got)
	}
	// 5. HSTS（仅生产）
	hsts := h.Get("Strict-Transport-Security")
	if !strings.Contains(hsts, "max-age=31536000") {
		t.Errorf("HSTS missing max-age=31536000: %q", hsts)
	}
	if !strings.Contains(hsts, "includeSubDomains") {
		t.Errorf("HSTS missing includeSubDomains: %q", hsts)
	}
	// 6. Permissions-Policy
	pp := h.Get("Permissions-Policy")
	if !strings.Contains(pp, "camera=()") {
		t.Errorf("Permissions-Policy missing camera=(): %q", pp)
	}
	if !strings.Contains(pp, "microphone=()") {
		t.Errorf("Permissions-Policy missing microphone=(): %q", pp)
	}
	if !strings.Contains(pp, "geolocation=()") {
		t.Errorf("Permissions-Policy missing geolocation=(): %q", pp)
	}
	// 7. X-XSS-Protection
	if got := h.Get("X-XSS-Protection"); got != "0" {
		t.Errorf("X-XSS-Protection = %q, want 0", got)
	}
}

// TestSecurityHeaders_NoHSTSInDev 验证开发环境不下发 HSTS。
func TestSecurityHeaders_NoHSTSInDev(t *testing.T) {
	w := runSecurityHeaders(t, false)
	if hsts := w.Header().Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("HSTS should not be set in dev, got: %q", hsts)
	}
	// 但其他 6 个 header 仍应下发。
	for _, name := range []string{
		"Content-Security-Policy",
		"X-Frame-Options",
		"X-Content-Type-Options",
		"Referrer-Policy",
		"Permissions-Policy",
		"X-XSS-Protection",
	} {
		if w.Header().Get(name) == "" {
			t.Errorf("dev should still set %s", name)
		}
	}
}

// TestSecurityHeaders_Order 验证 SecurityHeaders 不影响下游 handler 写入的 body。
func TestSecurityHeaders_DoesNotInterfereWithBody(t *testing.T) {
	w := runSecurityHeaders(t, true)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Errorf("body should pass through: %s", w.Body.String())
	}
}

// TestSecurityHeaders_PassesThroughDownstream 验证下游 handler 可继续写自己的 header。
func TestSecurityHeaders_PassesThroughDownstream(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "value-from-handler")
		w.WriteHeader(http.StatusTeapot)
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	SecurityHeaders(true)(h).ServeHTTP(w, r)
	if w.Header().Get("X-Custom") != "value-from-handler" {
		t.Errorf("downstream header lost: X-Custom")
	}
	if w.Code != http.StatusTeapot {
		t.Errorf("status = %d, want 418", w.Code)
	}
}

// TestSecurityHeaders_CSPHasRequiredDirectives 验证 CSP 包含所有必要 directive。
func TestSecurityHeaders_CSPHasRequiredDirectives(t *testing.T) {
	csp := buildCSP()
	required := []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"img-src 'self'",
		"font-src 'self'",
		"connect-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}
	for _, d := range required {
		if !strings.Contains(csp, d) {
			t.Errorf("CSP missing %q in %q", d, csp)
		}
	}
}

// TestSecurityHeaders_CSPHasUnsafeInlineForTailwindJIT 验证 unsafe-inline 存在
// （Tailwind JIT 必须 inline style）。
func TestSecurityHeaders_CSPHasUnsafeInlineForTailwindJIT(t *testing.T) {
	csp := buildCSP()
	if !strings.Contains(csp, "style-src 'self' 'unsafe-inline'") {
		t.Errorf("CSP must allow unsafe-inline style for Tailwind JIT: %q", csp)
	}
	if !strings.Contains(csp, "script-src 'self' 'unsafe-inline' 'unsafe-eval'") {
		t.Errorf("CSP must allow unsafe-inline/eval script (v2.2 accepted): %q", csp)
	}
}