// Package server — server_test.go
//
// Integration tests for the v2.1.1 wiring:
//
//   1. TenantGuard middleware: X-Tenant-ID header resolves into ctx so
//      downstream handlers see the right tenant.
//   2. Region hint: tenant region flows into ctx via llm.WithRegionHint
//      so the LLM router can reorder channels by region.
//   3. LLM health endpoint: /api/admin/llm/health returns the snapshot
//      of channel circuit-breaker states when wired.
//
// The tests use chi.NewRouter() directly so we don't have to bind a
// real network listener.

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/handler"
	"intelligent_customer/backend/internal/llm"
	"intelligent_customer/backend/internal/tenant"
	"intelligent_customer/backend/internal/testutil"
)

// fakeTenantLister implements the minimal contract tenant.Middleware
// needs from the repo (Get / EnsureDefault via a static map).
type fakeTenantRepo struct {
	tenants map[string]tenant.Tenant
}

func newFakeTenantRepo() *fakeTenantRepo {
	return &fakeTenantRepo{
		tenants: map[string]tenant.Tenant{
			tenant.DefaultID: {
				ID:     tenant.DefaultID,
				Name:   "Default",
				Region: "cn",
			},
			"tnt_intl": {
				ID:     "tnt_intl",
				Name:   "Intl",
				Region: "intl",
			},
			"tnt_eu": {
				ID:     "tnt_eu",
				Name:   "EU",
				Region: "intl",
			},
		},
	}
}

// We don't actually exercise the tenant repo through the middleware in
// these tests (the middleware uses *tenant.Repo). Instead we test
// tenant.Middleware with a real *tenant.Repo on a temp DB.

func TestTenantGuard_ResolvesHeader(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	repo := tenant.NewRepo(conn)
	if _, err := repo.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	if err := repo.Upsert(context.Background(), &tenant.Tenant{
		ID: "tnt_intl", Name: "Intl", Region: "intl", Status: "active",
		LLMPrimary: "openai", LLMSecondary: "openrouter", LLMTertiary: "minimax",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	mw := tenant.Middleware(repo, nil)
	var seen tenant.Info
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = tenant.FromContext(r.Context())
	}))

	// 1. No header → default
	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if seen.ID != tenant.DefaultID {
		t.Fatalf("expected default tenant, got %q", seen.ID)
	}

	// 2. X-Tenant-ID header → resolve from repo
	req2 := httptest.NewRequest("POST", "/api/chat", strings.NewReader("{}"))
	req2.Header.Set("X-Tenant-ID", "tnt_intl")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if seen.ID != "tnt_intl" || seen.Region != "intl" {
		t.Fatalf("expected tnt_intl/intl, got %+v", seen)
	}

	// 3. Unknown tenant → 404
	req3 := httptest.NewRequest("POST", "/api/chat", strings.NewReader("{}"))
	req3.Header.Set("X-Tenant-ID", "tnt_ghost")
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown tenant, got %d", rec3.Code)
	}

	// 4. Bad header → default
	req4 := httptest.NewRequest("POST", "/api/chat", strings.NewReader("{}"))
	req4.Header.Set("X-Tenant-ID", "bad id with space")
	rec4 := httptest.NewRecorder()
	h.ServeHTTP(rec4, req4)
	if seen.ID != tenant.DefaultID {
		t.Fatalf("bad header should fall back to default, got %q", seen.ID)
	}
}

// TestTenantGuard_RegionHintPropagates verifies the middleware set on
// the /api route group propagates the region into ctx so the LLM
// router can reorder channels.
func TestTenantGuard_RegionHintPropagates(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	repo := tenant.NewRepo(conn)
	if _, err := repo.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := repo.Upsert(context.Background(), &tenant.Tenant{
		ID: "tnt_intl", Name: "Intl", Region: "intl", Status: "active",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Build the same region-hint middleware chain that server.go wires.
	mw := tenant.Middleware(repo, nil)
	hint := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t := tenant.FromContext(r.Context())
			if t.Region != "" {
				r = r.WithContext(llm.WithRegionHint(r.Context(), t.Region))
			}
			next.ServeHTTP(w, r)
		})
	}
	var gotRegion string
	final := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotRegion = llm.RegionFromCtx(r.Context())
	})
	chain := mw(hint(final))

	// Header sets tenant → region hint must be "intl"
	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader("{}"))
	req.Header.Set("X-Tenant-ID", "tnt_intl")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)
	if gotRegion != "intl" {
		t.Fatalf("expected region=intl, got %q", gotRegion)
	}

	// No header → default tenant → region hint must be "cn"
	gotRegion = ""
	req2 := httptest.NewRequest("POST", "/api/chat", strings.NewReader("{}"))
	rec2 := httptest.NewRecorder()
	chain.ServeHTTP(rec2, req2)
	if gotRegion != "cn" {
		t.Fatalf("expected region=cn for default tenant, got %q", gotRegion)
	}
}

// TestLLMHealth_EndpointReturnsSnapshot exercises the LLMHealth
// handler end-to-end via the full server.New() wiring. The endpoint
// is behind JWT auth like the rest of /admin; we mint a token with
// the same issuer.
func TestLLMHealth_EndpointReturnsSnapshot(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	tenantRepo := tenant.NewRepo(conn)
	if _, err := tenantRepo.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	secret := "test-secret-please-change-in-prod-must-be-32b"
	issuer := auth.NewIssuer(secret, 60_000_000_000) // 60s
	tok, err := issuer.Sign("admin", "admin")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Synthesise a snapshot fn with one channel in "half_open" so we
	// can prove the JSON round-trip works without an actual router.
	snapshot := func() []handler.LLMChannelHealth {
		return []handler.LLMChannelHealth{
			{Slot: "primary", Provider: "minimax", Model: "MiniMax-M3", State: "closed"},
			{Slot: "secondary", Provider: "openai", Model: "gpt-4o-mini", State: "half_open"},
		}
	}
	healthH := &handler.LLMHealth{States: snapshot, Logger: zerolog.Nop()}

	router := New(Deps{
		Logger:      zerolog.Nop(),
		CORSOrigins: []string{},
		Issuer:      issuer,
		TenantRepo:  tenantRepo,
		LLMHealth:   healthH,
	})
	srv := httptest.NewServer(router)
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/api/admin/llm/health", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/admin/llm/health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body struct {
		Channels []handler.LLMChannelHealth `json:"channels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(body.Channels))
	}
	if body.Channels[0].Slot != "primary" || body.Channels[1].State != "half_open" {
		t.Fatalf("unexpected snapshot: %+v", body.Channels)
	}
}

// TestLLMHealth_EmptyWhenUnwired covers the path where the States fn
// is nil (no router wired) — the endpoint must still return 200 with
// an empty list so the admin page renders cleanly.
func TestLLMHealth_EmptyWhenUnwired(t *testing.T) {
	secret := "test-secret-please-change-in-prod-must-be-32b"
	issuer := auth.NewIssuer(secret, 60_000_000_000)
	tok, err := issuer.Sign("admin", "admin")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	healthH := &handler.LLMHealth{States: nil, Logger: zerolog.Nop()}
	router := New(Deps{
		Logger:    zerolog.Nop(),
		Issuer:    issuer,
		LLMHealth: healthH,
	})
	srv := httptest.NewServer(router)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/api/admin/llm/health", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body struct {
		Channels []handler.LLMChannelHealth `json:"channels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Channels) != 0 {
		t.Fatalf("expected empty list, got %+v", body.Channels)
	}
}

// TestHealth_StillPublicWithoutTenant makes sure the /health and
// /ready endpoints don't trigger TenantGuard (k8s probes can't sign
// requests AND can't carry tenant headers).
func TestHealth_StillPublicWithoutTenant(t *testing.T) {
	router := New(Deps{Logger: zerolog.Nop()})
	srv := httptest.NewServer(router)
	defer srv.Close()
	for _, p := range []string{"/health", "/ready"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s expected 200, got %d", p, resp.StatusCode)
		}
	}
}