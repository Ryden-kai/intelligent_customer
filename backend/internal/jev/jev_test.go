package jev

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/config"
	"intelligent_customer/backend/internal/model"
)

// newTestServer spins up an httptest server that mimics the Jev /alpha/decisions
// shape and returns the canned answer supplied by the test.
func newTestServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(handle))
	return srv, srv.Close
}

func newClient(t *testing.T, baseURL, apiKey string) *Client {
	t.Helper()
	cfg := config.Config{
		JEVBaseURL: baseURL,
		JEVAPIKey:  apiKey,
		JEVModel:   "test/jev",
	}
	return New(cfg, zerolog.Nop())
}

func TestClassifyIntent(t *testing.T) {
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/alpha/decisions") {
			t.Fatalf("bad path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Fatalf("missing bearer: %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "test/jev-1",
			"answers": map[string]any{
				"intent": map[string]any{
					"type":     "choice",
					"choice":   "refund",
					"confidence": 0.91,
				},
			},
			"usage": map[string]any{"cost": 0.0001},
		})
	})
	defer cleanup()

	c := newClient(t, srv.URL, "sk-test")
	got, conf, err := c.ClassifyIntent(context.Background(), "我要退款", nil)
	if err != nil {
		t.Fatalf("ClassifyIntent: %v", err)
	}
	if got != model.IntentRefund {
		t.Fatalf("intent: got %q want refund", got)
	}
	if conf < 0.9 {
		t.Fatalf("confidence too low: %f", conf)
	}
}

func TestNeedsHandover(t *testing.T) {
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{
				"handover": map[string]any{
					"type": "noul",
					"noul": 0.83,
				},
			},
		})
	})
	defer cleanup()
	c := newClient(t, srv.URL, "sk-test")
	need, conf, err := c.NeedsHandover(context.Background(), "找你们经理来", nil)
	if err != nil {
		t.Fatalf("NeedsHandover: %v", err)
	}
	if !need {
		t.Fatalf("expected need=true")
	}
	if conf < 0.8 {
		t.Fatalf("confidence low: %f", conf)
	}
}

func TestRateSatisfactionEmptySkips(t *testing.T) {
	c := newClient(t, "http://unused", "x")
	r, conf, err := c.RateSatisfaction(context.Background(), "")
	if err != nil || r != 0 || conf != 0 {
		t.Fatalf("expected zero values for empty comment, got r=%d conf=%f err=%v", r, conf, err)
	}
}

func TestRateSatisfactionBucket(t *testing.T) {
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{
				"satisfaction": map[string]any{
					"type":       "score",
					"score":      4.7, // → bucket 5
					"confidence": 0.6,
				},
			},
		})
	})
	defer cleanup()
	c := newClient(t, srv.URL, "sk-test")
	r, conf, err := c.RateSatisfaction(context.Background(), "挺好的")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r != 5 {
		t.Fatalf("bucket: got %d want 5", r)
	}
	if conf < 0.5 {
		t.Fatalf("confidence: %f", conf)
	}
}

func TestEnabled(t *testing.T) {
	c := New(config.Config{}, zerolog.Nop())
	if c.Enabled() {
		t.Fatalf("expected disabled when config empty")
	}
}

func TestDisabledError(t *testing.T) {
	c := New(config.Config{}, zerolog.Nop())
	_, _, err := c.ClassifyIntent(context.Background(), "x", nil)
	if err == nil {
		t.Fatalf("expected error when disabled")
	}
}

func TestHTTPErrorPropagates(t *testing.T) {
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	})
	defer cleanup()
	c := newClient(t, srv.URL, "sk-test")
	_, _, err := c.ClassifyIntent(context.Background(), "x", nil)
	if err == nil {
		t.Fatalf("expected error on http 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("err should mention status: %v", err)
	}
}

// avoid unused import warnings if time is removed later
var _ = time.Second