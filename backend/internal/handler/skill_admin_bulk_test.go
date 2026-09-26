// Package handler — skill_admin_bulk_test.go
//
// Tests for v2.2 PR5 skill bulk toggle endpoint:
//   - POST /api/admin/skills/toggle-batch

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/model"
	"intelligent_customer/backend/internal/skill"
)

func newSkillBulkRouter(h *SkillAdmin, issuer *auth.Issuer) http.Handler {
	r := chi.NewRouter()
	r.Use(requireAuthForTest(issuer))
	r.Post("/api/admin/skills/toggle-batch", h.ToggleBatch)
	return r
}

func newSkillBulkFixture(t *testing.T) (*SkillAdmin, string) {
	t.Helper()
	conn, _ := openTestDB(t)
	skillsRepo := skill.NewSkills(conn)
	reg := skill.NewRegistry()
	h := &SkillAdmin{
		Repo:        skillsRepo,
		Registry:    reg,
		FSDir:       "",
		Logger:      zerolog.Nop(),
	}
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	tok, _ := issuer.Sign("admin", "admin")
	return h, tok
}

func seedSkills(t *testing.T, repo *skill.Skills, names []string, enabled bool) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, 0, len(names))
	for _, n := range names {
		s := &model.Skill{
			Name:           n,
			Description:    n + " desc",
			Category:       "general",
			ParametersJSON: `{"type":"object","properties":{}}`,
			HandlerKind:    "echo",
			HandlerConfig:  "{}",
			Enabled:        enabled,
			RequiresHuman:  false,
			ReadOnly:       true,
		}
		if err := repo.Create(ctx, s); err != nil {
			t.Fatalf("create skill: %v", err)
		}
		ids = append(ids, s.ID)
	}
	return ids
}

// ---------------------------------------------------------------------------
// ToggleBatch
// ---------------------------------------------------------------------------

func TestSkillAdmin_ToggleBatch_EnablesAll(t *testing.T) {
	h, tok := newSkillBulkFixture(t)
	ids := seedSkills(t, h.Repo, []string{"s1", "s2", "s3"}, false)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	router := newSkillBulkRouter(h, issuer)

	body, _ := json.Marshal(map[string]any{"ids": ids, "enabled": true})
	rr := callJSON(router, "POST", "/api/admin/skills/toggle-batch", string(body), tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Total     int               `json:"total"`
		Succeeded int               `json:"succeeded"`
		Enabled   bool              `json:"enabled"`
		Results   []batchItemResult `json:"results"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Succeeded != 3 {
		t.Fatalf("expected 3 succeeded, got %d", resp.Succeeded)
	}
	if !resp.Enabled {
		t.Fatalf("expected enabled=true in response")
	}
}

func TestSkillAdmin_ToggleBatch_DisablesAll(t *testing.T) {
	h, tok := newSkillBulkFixture(t)
	ids := seedSkills(t, h.Repo, []string{"s1", "s2"}, true)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	router := newSkillBulkRouter(h, issuer)

	body, _ := json.Marshal(map[string]any{"ids": ids, "enabled": false})
	rr := callJSON(router, "POST", "/api/admin/skills/toggle-batch", string(body), tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp struct {
		Succeeded int `json:"succeeded"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Succeeded != 2 {
		t.Fatalf("expected 2 succeeded, got %d", resp.Succeeded)
	}
}

func TestSkillAdmin_ToggleBatch_Idempotent(t *testing.T) {
	h, tok := newSkillBulkFixture(t)
	// Skills already enabled → second toggle should be a no-op (unchanged).
	ids := seedSkills(t, h.Repo, []string{"s1", "s2"}, true)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	router := newSkillBulkRouter(h, issuer)

	body, _ := json.Marshal(map[string]any{"ids": ids, "enabled": true})
	rr := callJSON(router, "POST", "/api/admin/skills/toggle-batch", string(body), tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp struct {
		Succeeded int               `json:"succeeded"`
		Results   []batchItemResult `json:"results"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Succeeded != 0 {
		t.Fatalf("expected 0 succeeded (idempotent no-op), got %d", resp.Succeeded)
	}
	for _, r := range resp.Results {
		if r.Status != "unchanged" {
			t.Fatalf("expected unchanged, got %+v", r)
		}
	}
}

func TestSkillAdmin_ToggleBatch_UnknownIDs(t *testing.T) {
	h, tok := newSkillBulkFixture(t)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	router := newSkillBulkRouter(h, issuer)

	body, _ := json.Marshal(map[string]any{"ids": []string{"nope"}, "enabled": true})
	rr := callJSON(router, "POST", "/api/admin/skills/toggle-batch", string(body), tok)
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

func TestSkillAdmin_ToggleBatch_RejectsEmptyIDs(t *testing.T) {
	h, tok := newSkillBulkFixture(t)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	router := newSkillBulkRouter(h, issuer)

	body, _ := json.Marshal(map[string]any{"ids": []string{}, "enabled": true})
	rr := callJSON(router, "POST", "/api/admin/skills/toggle-batch", string(body), tok)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty ids, got %d", rr.Code)
	}
}

func TestSkillAdmin_ToggleBatch_RejectsTooMany(t *testing.T) {
	h, tok := newSkillBulkFixture(t)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	router := newSkillBulkRouter(h, issuer)

	ids := make([]string, 201)
	for i := range ids {
		ids[i] = "id-" + string(rune('a'+i%26))
	}
	body, _ := json.Marshal(map[string]any{"ids": ids, "enabled": true})
	rr := callJSON(router, "POST", "/api/admin/skills/toggle-batch", string(body), tok)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for too many ids, got %d", rr.Code)
	}
}

// keep strings imported
var _ = strings.TrimSpace