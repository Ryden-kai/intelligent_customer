// Package middleware — sanitize.go
//
// 错误脱敏中间件（v2.2 PR1）：包装 5xx 响应，确保生产环境不泄露内部信息。
//
// 设计要点：
//   1. 仅处理 5xx 响应（4xx 是业务错误，按设计就是给用户看的，保持原样）。
//   2. 生产环境（env="production"）：替换 body 为通用 JSON
//      `{ code: "internal", message: "服务暂时不可用，请稍后再试", request_id }`，
//      不暴露 stack / 内部 error 文本。
//   3. 开发环境：保留原始错误体 + stack（如果有 panic）。
//   4. 5xx 的具体写入走 deferred recover()：既处理 panic，也处理 handler 主动
//      writeAppError 返回 5xx 的情况。
//   5. 不影响 status code / Content-Type 之外的 header（如 X-Request-Id 保留）。
//
// 与现有 Recover middleware 的关系：
//   - Recover 仅记录 panic 日志（含 stack），返回通用 500。
//   - Sanitize 是"包装层"：在 Sanitize 内部的任何 handler panic / 5xx 都会被
//     它拦截并按 env 决定脱敏策略。
//   - 使用方式：把 Sanitize 挂到 Recover 之后、handler 之前。
package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"

	"intelligent_customer/backend/internal/apperr"
)

// Sanitize 返回一个中间件：包装 5xx 响应，按 env 决定脱敏策略。
//
// env 取值：
//   - "production" → 生产模式：5xx body 统一为 { code, message, request_id }
//   - 其他（含 "" / "development"） → 开发模式：保留原始 5xx body + stack
//
// 调用链顺序（推荐）：RequestID → Recover → Sanitize → AccessLog → ...
//
// 注意：Sanitize 内部也做 panic recover，所以即使外层 Recover 被绕过也不会泄露。
func Sanitize(env string) func(http.Handler) http.Handler {
	isProd := env == "production"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := newSanitizeWriter(w)
			defer func() {
				if rec := recover(); rec != nil {
					// handler panic：把 500 写出去。
					emitSanitizedPanic(sw, isProd, rec)
				}
			}()
			next.ServeHTTP(sw, r)
			// 正常返回路径：如果 handler 已经写了 5xx 但没 panic，做一次脱敏。
			if sw.shouldFlush() {
				flushSanitized(sw, isProd)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// sanitizeWriter：捕获 ResponseWriter 的 status / body / headers，在需要时
// 拦截并替换 5xx 输出。
// ----------------------------------------------------------------------------

type sanitizeWriter struct {
	orig http.ResponseWriter

	// 状态码：第一次 WriteHeader 时设定。
	status int
	// 是否已经开始向 orig 写入 header：false = 还在缓冲。
	headerCommitted bool
	// 缓冲 body（仅 status >= 500 时启用）。
	body bytes.Buffer
	// 捕获到的 orig headers（用于恢复 Content-Type 等关键头）。
	capturedHeaders http.Header
}

func newSanitizeWriter(orig http.ResponseWriter) *sanitizeWriter {
	return &sanitizeWriter{
		orig:            orig,
		capturedHeaders: http.Header{},
	}
}

// Header 允许上游在 WriteHeader 之前设置 headers。我们把这些 header 暂存
// 到 capturedHeaders；只有最终决定 flush 时才把它们写回 orig。
func (s *sanitizeWriter) Header() http.Header {
	if s.headerCommitted {
		return s.orig.Header()
	}
	return s.capturedHeaders
}

// WriteHeader 捕获 status code。status >= 500 时进入"缓冲模式"——不立即写
// 到 orig，而是把 body 写到内部 buffer。
//
// 遵循 net/http 约定：多次调用 WriteHeader 时，第一次的 code 生效，后续忽略。
func (s *sanitizeWriter) WriteHeader(code int) {
	if s.headerCommitted || s.status != 0 {
		// 已写过 header 或已有 status；按 net/http 约定忽略后续调用。
		return
	}
	s.status = code
	if code >= 500 {
		// 缓冲模式：保留 status 和 body，等 flush 阶段决定是否脱敏。
		return
	}
	// 4xx 及以下：直接透传，把 captured headers 同步到 orig。
	s.commitHeaders()
	s.orig.WriteHeader(code)
	s.headerCommitted = true
}

// Write 把字节写入缓冲（5xx）或直接透传到 orig（< 5xx）。
func (s *sanitizeWriter) Write(b []byte) (int, error) {
	// 仅当 status 尚未设置时（即 handler 完全没调 WriteHeader）才补一个 200。
	// 如果 status 已被设为 5xx（buffer 模式），不能覆盖。
	if s.status == 0 && !s.headerCommitted {
		s.WriteHeader(http.StatusOK)
	}
	if s.status >= 500 {
		return s.body.Write(b)
	}
	return s.orig.Write(b)
}

// shouldFlush 报告中间件是否需要在 ServeHTTP 返回后做收尾。
func (s *sanitizeWriter) shouldFlush() bool {
	return s.status >= 500 && !s.headerCommitted
}

// commitHeaders 把 captured headers 同步到 orig。
func (s *sanitizeWriter) commitHeaders() {
	if s.headerCommitted {
		return
	}
	h := s.orig.Header()
	for k, v := range s.capturedHeaders {
		// 不覆盖已经有值的 header（保留 RequestID middleware 设置的 X-Request-Id）。
		if h.Get(k) == "" {
			for _, val := range v {
				h.Add(k, val)
			}
		}
	}
}

// flushSanitized 把缓冲的 5xx body 写入 orig，按 env 决定是否脱敏。
func flushSanitized(sw *sanitizeWriter, isProd bool) {
	h := sw.orig.Header()
	// 清掉所有 captured headers（避免泄露非预期 header）。
	for k := range sw.capturedHeaders {
		// 保留 X-Request-Id 和其它由 RequestID middleware 写入的 header。
		if k == "X-Request-Id" {
			continue
		}
		delete(sw.capturedHeaders, k)
	}
	// 重新合并 headers（先 orig，再 captured 中保留的）。
	sw.commitHeaders()
	h.Set("Content-Type", "application/json; charset=utf-8")

	if isProd {
		// 生产：通用 5xx body。
		body := sanitizedInternalBody(h.Get("X-Request-Id"))
		sw.orig.WriteHeader(sw.status)
		_, _ = sw.orig.Write(body)
		return
	}
	// 开发：保留原始 body。
	sw.orig.WriteHeader(sw.status)
	_, _ = sw.orig.Write(sw.body.Bytes())
}

// emitSanitizedPanic 处理 handler panic 的 5xx 写出。
func emitSanitizedPanic(sw *sanitizeWriter, isProd bool, rec any) {
	// 即使 Sanitize 在 Recover 之前挂，也得保证 panic 不外泄。
	h := sw.orig.Header()
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", "application/json; charset=utf-8")
	}
	// 清掉 captured headers（防止 handler 在 panic 前设置了敏感 header）。
	for k := range sw.capturedHeaders {
		if k == "X-Request-Id" {
			continue
		}
		delete(sw.capturedHeaders, k)
	}
	sw.commitHeaders()

	if !sw.headerCommitted {
		// 还没写过 header；现在写。
		sw.status = http.StatusInternalServerError
		sw.orig.WriteHeader(http.StatusInternalServerError)
		sw.headerCommitted = true
	}

	if isProd {
		body := sanitizedInternalBody(h.Get("X-Request-Id"))
		_, _ = sw.orig.Write(body)
		return
	}
	// 开发：写 code + message + stack。
	devBody := map[string]any{
		"code":    "internal",
		"message": fmt.Sprintf("%v", rec),
		"stack":   string(debug.Stack()),
	}
	enc := json.NewEncoder(sw.orig)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(devBody)
}

// sanitizedInternalBody 返回生产环境的通用 5xx 响应体。
// request_id 从 header 读取，保持与 RequestID middleware 一致。
func sanitizedInternalBody(requestID string) []byte {
	payload := struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id,omitempty"`
	}{
		Code:      string(apperr.CodeInternal),
		Message:   "服务暂时不可用，请稍后再试",
		RequestID: requestID,
	}
	b, _ := json.Marshal(payload)
	return b
}