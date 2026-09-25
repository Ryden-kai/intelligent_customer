package ratelimit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/ratelimit"
)

func TestMiddleware_AllowsWhenUnderThreshold(t *testing.T) {
	m, conn := newMap(t)
	// 调高 login 限制确保不被触限。
	if _, err := conn.Exec(
		`UPDATE rate_limit_configs SET per_minute=1000, burst=1000, updated_at=? WHERE endpoint='POST:/api/auth/login' AND dimension='ip'`,
		time.Now().UnixMilli()); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	h := ratelimit.Middleware(m, ratelimit.DimensionIP, "POST:/api/auth/login")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader([]byte("{}")))
	req.Header.Set("X-Forwarded-For", "1.1.1.1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status=%d, want 200", w.Code)
	}
}

func TestMiddleware_Returns429AndRetryAfter(t *testing.T) {
	m, conn := newMap(t)
	// 把 login 阈值压到 burst=1，便于触发 429。
	if _, err := conn.Exec(
		`UPDATE rate_limit_configs SET per_minute=1, burst=1, updated_at=? WHERE endpoint='POST:/api/auth/login' AND dimension='ip'`,
		time.Now().UnixMilli()); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	var downstreamCalls int
	h := ratelimit.Middleware(m, ratelimit.DimensionIP, "POST:/api/auth/login")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			downstreamCalls++
			w.WriteHeader(http.StatusOK)
		}),
	)
	makeReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader([]byte("{}")))
		req.Header.Set("X-Forwarded-For", "5.5.5.5")
		return req
	}

	// 第一次 → 200（pass），handler 被调用 1 次。
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, makeReq())
	if w1.Code != http.StatusOK {
		t.Fatalf("first call status=%d, want 200", w1.Code)
	}
	if downstreamCalls != 1 {
		t.Errorf("after first call downstream calls = %d, want 1", downstreamCalls)
	}

	// 第二次 → 429，handler 不应被调用。
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, makeReq())
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second call status=%d, want 429", w2.Code)
	}
	if downstreamCalls != 1 {
		t.Errorf("after second call downstream calls = %d, want 1 (handler must NOT be called when blocked)", downstreamCalls)
	}
	ra := w2.Header().Get("Retry-After")
	if ra == "" {
		t.Errorf("Retry-After header missing")
	} else {
		n, err := strconv.Atoi(ra)
		if err != nil || n < 1 {
			t.Errorf("Retry-After=%q, want positive int seconds", ra)
		}
	}
	// body 形如 {"code":"rate_limit_exceeded",...}
	var body map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &body); err != nil {
		t.Errorf("body parse: %v", err)
	}
	if body["code"] != "rate_limit_exceeded" {
		t.Errorf("code=%v", body["code"])
	}
}

func TestMiddleware_EmptyDimensionPassesThrough(t *testing.T) {
	m, _ := newMap(t)
	h := ratelimit.Middleware(m, ratelimit.DimensionActorID, "POST:/api/auth/login")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)
	// 没有 ctx claims，actor_id 取不到 → 应直接放行。
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status=%d, want 200 (no actor should pass)", w.Code)
	}
}

func TestMiddleware_ActorDimension(t *testing.T) {
	m, conn := newMap(t)
	if _, err := conn.Exec(
		`UPDATE rate_limit_configs SET per_minute=1, burst=1, updated_at=? WHERE endpoint='ALL:/api/admin' AND dimension='actor_id'`,
		time.Now().UnixMilli()); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	h := ratelimit.Middleware(m, ratelimit.DimensionActorID, "ALL:/api/admin")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)
	makeReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/x", nil)
		c := &auth.Claims{Username: "alice"}
		return req.WithContext(auth.WithClaims(req.Context(), c))
	}

	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, makeReq())
	if w1.Code != http.StatusOK {
		t.Fatalf("first call status=%d", w1.Code)
	}
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, makeReq())
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second call status=%d, want 429", w2.Code)
	}
}