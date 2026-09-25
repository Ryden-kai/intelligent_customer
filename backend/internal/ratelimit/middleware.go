package ratelimit

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/middleware"
	"intelligent_customer/backend/internal/tenant"
)

// Middleware 构造 HTTP 中间件：对指定 endpoint key 触发限流。
//
// dimension ∈ {ip, tenant_id, actor_id}：
//   - ip：取 ClientIP(r)
//   - tenant_id：取 tenant.FromContext(r.Context()).ID
//   - actor_id：取 auth.FromContext(r.Context()).Username
//
// 任一维度值缺失时直接放行（防误封）。
//
// 触发 429 时附加 Retry-After header + 标准化 JSON body（与 apperr.RateLimited
// 形状一致：{ code, message, retry_after }）。
func Middleware(limiter *LimiterMap, dimension Dimension, endpoint string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			value := extractDimensionValue(r, dimension)
			if value == "" {
				next.ServeHTTP(w, r)
				return
			}
			res := limiter.Allow(dimension, value, endpoint)
			if res.OK {
				next.ServeHTTP(w, r)
				return
			}
			write429(w, res.RetryAfter)
		})
	}
}

func extractDimensionValue(r *http.Request, dim Dimension) string {
	switch dim {
	case DimensionIP:
		return middleware.ClientIP(r)
	case DimensionTenantID:
		t := tenant.FromContext(r.Context())
		return t.ID
	case DimensionActorID:
		c, ok := auth.FromContext(r.Context())
		if !ok || c == nil {
			return ""
		}
		return c.Username
	}
	return ""
}

// write429 写 429 + Retry-After + 标准 JSON。
func write429(w http.ResponseWriter, retryAfter time.Duration) {
	if retryAfter <= 0 {
		retryAfter = time.Second
	}
	// 至少 1s，避免 header 为 0。
	secs := int64(retryAfter.Seconds())
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(secs, 10))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	body := map[string]any{
		"code":        "rate_limit_exceeded",
		"message":     "操作太频繁，请稍后再试",
		"retry_after": secs,
	}
	_ = json.NewEncoder(&nopWriter{w: w}).Encode(body)
}

// nopWriter 避免 http.ResponseWriter 在 bytes.Buffer 与原始 w 之间再拷贝。
type nopWriter struct {
	w http.ResponseWriter
}

func (n *nopWriter) Write(p []byte) (int, error) {
	return n.w.Write(p)
}

// bytes import keep referenced
var _ = bytes.Buffer{}