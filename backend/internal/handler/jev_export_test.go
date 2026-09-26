// Package handler — jev_export_test.go
//
// Tests for v2.2 PR5 Jev admin export + bulk endpoints:
//   - GET  /api/admin/jev/decisions/export
//   - POST /api/admin/jev/templates/archive-batch

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/tenant"
)

func newJevExportRouter(h *JevAdmin, issuer *auth.Issuer) http.Handler {
	r := chi.NewRouter()
	r.Route("/api/admin/jev", func(r chi.Router) {
		r.Use(requireAuthForTest(issuer))
		r.Get("/decisions/export", h.ExportDecisions)
		r.Post("/templates/archive-batch", h.ArchiveTemplatesBatch)
	})
	return r
}

func seedJevDecisionsExport(t *testing.T, repo *jev.DecisionRepo, names []string) []string {
	t.Helper()
	ids := make([]string, 0, len(names))
	for _, name := range names {
		d := &jev.DecisionRecord{
			TenantID:        tenant.DefaultID,
			TemplateName:    name,
			TemplateVersion: 1,
			Trigger:         jev.TriggerMessageEntry,
			InputHash:       "h-" + name,
			InputJSON:       `{"message":"q"}`,
			OutputJSON:      `{"label":"order"}`,
			LatencyMS:       50,
			Status:          "decided",
			TraceID:         "trace-" + name,
		}
		if err := repo.Insert(context.Background(), d); err != nil {
			t.Fatalf("insert decision: %v", err)
		}
		ids = append(ids, d.ID)
	}
	return ids
}

func seedJevTemplatesForExport(t *testing.T, repo *jev.TemplateRepo, names []string) []string {
	t.Helper()
	ids := make([]string, 0, len(names))
	for i, n := range names {
		rec := &jev.TemplateRecord{
			TenantID:     tenant.DefaultID,
			Name:         n,
			Version:      i + 1,
			Trigger:      jev.TriggerMessageEntry,
			OutputType:   jev.OutputChoice,
			Labels:       []string{"a"},
			Instructions: "x",
			Status:       "draft",
		}
		if err := repo.Create(context.Background(), rec); err != nil {
			t.Fatalf("create template: %v", err)
		}
		ids = append(ids, rec.ID)
	}
	return ids
}

// signAndRouter builds an issuer with the same secret as the fixture
// so the token is accepted by requireAuthForTest. We give the issuer a
// generous TTL (24h) because TTL=0 produces ExpiresAt == IssuedAt and
// the default jwt leeway may treat it as expired.
func signAndRouter(h *JevAdmin, t *testing.T) (http.Handler, string) {
	t.Helper()
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	tok, err := issuer.Sign("admin", "admin")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return newJevExportRouter(h, issuer), tok
}

// ---------------------------------------------------------------------------
// ExportDecisions
// ---------------------------------------------------------------------------

func TestJevAdmin_ExportDecisions_WritesCSVWithBOM(t *testing.T) {
	h, _, _ := newJevAdminFixture(t)
	seedJevDecisionsExport(t, h.DecisionRepo, []string{"intent_routing", "intent_routing", "intent_routing"})
	router, tok := signAndRouter(h, t)

	rr := callJSON(router, "GET", "/api/admin/jev/decisions/export", "", tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Fatalf("expected text/csv content-type, got %q", got)
	}
	body := rr.Body.String()
	if !strings.HasPrefix(body, "\xEF\xBB\xBF") {
		t.Fatalf("expected UTF-8 BOM at start, got %q", body[:min(8, len(body))])
	}
	if !strings.Contains(body, "id,template_name") {
		t.Fatalf("expected header row, got %q", body[:min(120, len(body))])
	}
	if !strings.Contains(body, "intent_routing") {
		t.Fatalf("expected row data, got %s", body)
	}
}

func TestJevAdmin_ExportDecisions_FilterByTemplate(t *testing.T) {
	h, _, _ := newJevAdminFixture(t)
	seedJevDecisionsExport(t, h.DecisionRepo, []string{"intent_routing", "intent_routing", "sensitive_check"})
	router, tok := signAndRouter(h, t)

	rr := callJSON(router, "GET", "/api/admin/jev/decisions/export?template=sensitive_check", "", tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Count(body, "sensitive_check") == 0 {
		t.Fatalf("expected sensitive_check row, body=%s", body)
	}
	lines := strings.Split(strings.TrimPrefix(body, "\xEF\xBB\xBF"), "\n")
	dataLines := 0
	for _, l := range lines[1:] {
		if strings.Contains(l, "intent_routing") {
			dataLines++
		}
	}
	if dataLines > 0 {
		t.Fatalf("filter should exclude intent_routing, found %d rows", dataLines)
	}
}

func TestJevAdmin_ExportDecisions_EmptyReturnsHeaderOnly(t *testing.T) {
	h, _, _ := newJevAdminFixture(t)
	router, tok := signAndRouter(h, t)

	rr := callJSON(router, "GET", "/api/admin/jev/decisions/export", "", tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	lines := strings.Split(strings.TrimPrefix(body, "\xEF\xBB\xBF"), "\n")
	// Expect header + trailing newline → 2 elements when split by '\n',
	// the second of which is empty. Filter empty strings to count data rows.
	dataRows := 0
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) != "" {
			dataRows++
		}
	}
	if dataRows != 0 {
		t.Fatalf("expected header-only when no data, got %d data rows", dataRows)
	}
}

// ---------------------------------------------------------------------------
// ArchiveTemplatesBatch
// ---------------------------------------------------------------------------

func TestJevAdmin_ArchiveBatch_ArchivesAll(t *testing.T) {
	h, _, _ := newJevAdminFixture(t)
	ids := seedJevTemplatesForExport(t, h.TemplateRepo, []string{"t1", "t2", "t3"})
	router, tok := signAndRouter(h, t)

	body, _ := json.Marshal(map[string]any{"ids": ids[:2]})
	rr := callJSON(router, "POST", "/api/admin/jev/templates/archive-batch", string(body), tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Total     int               `json:"total"`
		Succeeded int               `json:"succeeded"`
		Results   []batchItemResult `json:"results"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 2 || resp.Succeeded != 2 {
		t.Fatalf("expected 2/2 succeeded, got %+v", resp)
	}
	for _, id := range ids[:2] {
		got, err := h.TemplateRepo.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "archived" {
			t.Fatalf("id %s status = %q, want archived", id, got.Status)
		}
	}
	got, _ := h.TemplateRepo.Get(context.Background(), ids[2])
	if got.Status != "draft" {
		t.Fatalf("id %s should remain draft, got %q", ids[2], got.Status)
	}
}

func TestJevAdmin_ArchiveBatch_IdempotentOnUnknown(t *testing.T) {
	h, _, _ := newJevAdminFixture(t)
	router, tok := signAndRouter(h, t)

	body, _ := json.Marshal(map[string]any{"ids": []string{"nope", "still-nope"}})
	rr := callJSON(router, "POST", "/api/admin/jev/templates/archive-batch", string(body), tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp struct {
		Succeeded int               `json:"succeeded"`
		Results   []batchItemResult `json:"results"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Succeeded != 0 {
		t.Fatalf("expected 0 succeeded, got %d", resp.Succeeded)
	}
	for _, r := range resp.Results {
		if r.Status != "not_found" {
			t.Fatalf("expected not_found, got %+v", r)
		}
	}
}

func TestJevAdmin_ArchiveBatch_RejectsEmpty(t *testing.T) {
	h, _, _ := newJevAdminFixture(t)
	router, tok := signAndRouter(h, t)

	body, _ := json.Marshal(map[string]any{"ids": []string{}})
	rr := callJSON(router, "POST", "/api/admin/jev/templates/archive-batch", string(body), tok)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty ids, got %d", rr.Code)
	}
}

func TestJevAdmin_ArchiveBatch_RejectsTooMany(t *testing.T) {
	h, _, _ := newJevAdminFixture(t)
	router, tok := signAndRouter(h, t)

	ids := make([]string, 201)
	for i := range ids {
		ids[i] = "id-" + string(rune('a'+i%26))
	}
	body, _ := json.Marshal(map[string]any{"ids": ids})
	rr := callJSON(router, "POST", "/api/admin/jev/templates/archive-batch", string(body), tok)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for too many ids, got %d", rr.Code)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}