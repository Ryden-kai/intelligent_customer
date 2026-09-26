package middleware

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// panicHandler 触发一个 panic，用于测试 panic recover + 脱敏。
var panicHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	panic("simulated panic: SQL constraint SELECT * FROM users WHERE secret='hunter2'")
})

// sqlErrorHandler 模拟 handler 主动返回 500 含敏感信息。
var sqlErrorHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(`{"code":"internal","message":"SQLSTATE 23505: UNIQUE constraint failed: users.email","detail":"internal/users_repo.go:42"}`))
})

// nilDerefHandler 模拟 nil pointer dereference panic。
var nilDerefHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	var p *int
	_ = *p // nil deref
})

// ioErrorHandler 模拟 IO error panic。
var ioErrorHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	panic(errors.New("read tcp 1.2.3.4:80: i/o timeout"))
})

// timeoutHandler 模拟上游 timeout。
var timeoutHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	panic(fmt.Errorf("upstream timeout after 5s: %w", errors.New("context deadline exceeded")))
})

// thirdPartyAPIHandler 模拟第三方 API 错误。
var thirdPartyAPIHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	panic("openai API returned 502: server overloaded (key=sk-1234567890abcdef)")
})

// unauthorizedHandler 模拟 401（非 5xx，应该透传）。
var unauthorizedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"code":"unauthorized","message":"missing bearer token"}`))
})

// notFoundHandler 模拟 404。
var notFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"code":"not_found","message":"resource not found"}`))
})

// okHandler 模拟 200。
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
})

// forbiddenHandler 模拟 403。
var forbiddenHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"code":"forbidden","message":"权限不足"}`))
})

func runSanitize(t *testing.T, env string, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	Sanitize(env)(h).ServeHTTP(w, r)
	return w
}

// TestSanitize_ProdPanicNoLeak 验证生产模式下 panic 响应不泄露敏感信息。
func TestSanitize_ProdPanicNoLeak(t *testing.T) {
	w := runSanitize(t, "production", panicHandler)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	// 不应包含 panic 原文 / SQL 关键字 / stack。
	for _, leak := range []string{"hunter2", "SELECT", "panic", "goroutine", "runtime/debug", "simulated"} {
		if strings.Contains(body, leak) {
			t.Errorf("prod body leaks %q: %s", leak, body)
		}
	}
	// 必须包含通用 5xx 标识。
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["code"] != "internal" {
		t.Errorf("code = %v, want 'internal'", resp["code"])
	}
	if resp["message"] != "服务暂时不可用，请稍后再试" {
		t.Errorf("message = %v, want sanitized", resp["message"])
	}
}

// TestSanitize_ProdSQLErrorNoLeak 验证 handler 主动返回的 5xx 也被脱敏。
func TestSanitize_ProdSQLErrorNoLeak(t *testing.T) {
	w := runSanitize(t, "production", sqlErrorHandler)
	body := w.Body.String()
	for _, leak := range []string{"SQLSTATE", "23505", "users_repo.go", "constraint"} {
		if strings.Contains(body, leak) {
			t.Errorf("prod body leaks %q: %s", leak, body)
		}
	}
	if !strings.Contains(body, "服务暂时不可用") {
		t.Errorf("expected sanitized message, got: %s", body)
	}
}

// TestSanitize_DevPanicPreservesStack 验证开发模式保留 stack + 原文。
func TestSanitize_DevPanicPreservesStack(t *testing.T) {
	w := runSanitize(t, "development", panicHandler)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "simulated panic") {
		t.Errorf("dev body should contain panic text: %s", body)
	}
	if !strings.Contains(body, "stack") {
		t.Errorf("dev body should contain stack field: %s", body)
	}
	if !strings.Contains(body, "goroutine") {
		t.Errorf("dev body should contain stack trace (goroutine): %s", body)
	}
}

// TestSanitize_NilDerefProd 验证 nil deref panic 在 prod 被脱敏。
func TestSanitize_NilDerefProd(t *testing.T) {
	w := runSanitize(t, "production", nilDerefHandler)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(w.Body.String(), "服务暂时不可用") {
		t.Errorf("expected sanitized message: %s", w.Body.String())
	}
}

// TestSanitize_IOErrorProd 验证 IO error panic 在 prod 被脱敏。
func TestSanitize_IOErrorProd(t *testing.T) {
	w := runSanitize(t, "production", ioErrorHandler)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	for _, leak := range []string{"1.2.3.4", "tcp", "i/o", "timeout"} {
		if strings.Contains(body, leak) {
			t.Errorf("prod body leaks %q: %s", leak, body)
		}
	}
}

// TestSanitize_TimeoutProd 验证 timeout panic 在 prod 被脱敏。
func TestSanitize_TimeoutProd(t *testing.T) {
	w := runSanitize(t, "production", timeoutHandler)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	for _, leak := range []string{"deadline", "upstream", "context"} {
		if strings.Contains(body, leak) {
			t.Errorf("prod body leaks %q: %s", leak, body)
		}
	}
}

// TestSanitize_ThirdPartyAPIProd 验证第三方 API key 泄露被拦截。
func TestSanitize_ThirdPartyAPIProd(t *testing.T) {
	w := runSanitize(t, "production", thirdPartyAPIHandler)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	for _, leak := range []string{"sk-1234567890abcdef", "openai", "overloaded"} {
		if strings.Contains(body, leak) {
			t.Errorf("prod body leaks %q: %s", leak, body)
		}
	}
}

// TestSanitize_UnauthorizedPreserved 验证 401 不被脱敏（4xx 是业务错误，按设计暴露）。
func TestSanitize_UnauthorizedPreserved(t *testing.T) {
	w := runSanitize(t, "production", unauthorizedHandler)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "missing bearer token") {
		t.Errorf("401 message should pass through: %s", body)
	}
	if !strings.Contains(body, "unauthorized") {
		t.Errorf("401 code should pass through: %s", body)
	}
}

// TestSanitize_ForbiddenPreserved 验证 403 不被脱敏。
func TestSanitize_ForbiddenPreserved(t *testing.T) {
	w := runSanitize(t, "production", forbiddenHandler)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "权限不足") {
		t.Errorf("403 message should pass through: %s", body)
	}
}

// TestSanitize_NotFoundPreserved 验证 404 不被脱敏。
func TestSanitize_NotFoundPreserved(t *testing.T) {
	w := runSanitize(t, "production", notFoundHandler)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "resource not found") {
		t.Errorf("404 message should pass through: %s", body)
	}
}

// TestSanitize_OKPreserved 验证 200 不被脱敏。
func TestSanitize_OKPreserved(t *testing.T) {
	w := runSanitize(t, "production", okHandler)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Errorf("200 body should pass through: %s", w.Body.String())
	}
}

// TestSanitize_DevSQLErrorPreservesDetail 验证开发模式保留 SQL 错误细节（便于调试）。
func TestSanitize_DevSQLErrorPreservesDetail(t *testing.T) {
	w := runSanitize(t, "development", sqlErrorHandler)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "SQLSTATE") {
		t.Errorf("dev body should retain SQL details: %s", body)
	}
}

// TestSanitize_DefaultEnvIsDevelopment 验证 env="" 默认走开发模式（保留 stack）。
func TestSanitize_DefaultEnvIsDevelopment(t *testing.T) {
	w := runSanitize(t, "", panicHandler)
	if !strings.Contains(w.Body.String(), "simulated panic") {
		t.Errorf("default env should preserve panic text: %s", w.Body.String())
	}
}

// TestSanitize_ProdNoStackOnHandlerReturn 验证 handler 主动写 5xx（非 panic），
// prod 模式下不暴露 stack（stack 只在 panic 时出现）。
func TestSanitize_ProdNoStackOnHandlerReturn(t *testing.T) {
	w := runSanitize(t, "production", sqlErrorHandler)
	body := w.Body.String()
	if strings.Contains(body, "stack") {
		t.Errorf("prod body should NOT contain 'stack' field for non-panic 5xx: %s", body)
	}
}

// TestSanitize_RequestIDPreserved 验证 RequestID middleware 设置的 header 在脱敏后保留。
func TestSanitize_RequestIDPreserved(t *testing.T) {
	// 模拟 RequestID middleware：先注入 X-Request-Id，再跑下游。
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "req-test-12345")
		panic("boom")
	})
	w := runSanitize(t, "production", h)
	if got := w.Header().Get("X-Request-Id"); got != "req-test-12345" {
		t.Errorf("X-Request-Id = %q, want 'req-test-12345'", got)
	}
}

// TestSanitize_InvalidEnvTreatedAsDevelopment 验证 env 取非法值时降级为开发模式。
func TestSanitize_InvalidEnvTreatedAsDevelopment(t *testing.T) {
	w := runSanitize(t, "staging", panicHandler)
	// staging 不是 production，保留 stack。
	if !strings.Contains(w.Body.String(), "simulated panic") {
		t.Errorf("staging should preserve panic text: %s", w.Body.String())
	}
}

// TestSanitize_NoRecursionOnWriteHeader 验证连续调用 WriteHeader 不会破坏（按 net/http 规范）。
func TestSanitize_NoRecursionOnWriteHeader(t *testing.T) {
	doubleHeader := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.WriteHeader(http.StatusOK) // 不应 panic；httptest.NewRecorder 会自动忽略
	})
	w := runSanitize(t, "production", doubleHeader)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}