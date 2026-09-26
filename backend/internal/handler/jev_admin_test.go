package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/tenant"
	"intelligent_customer/backend/internal/testutil"
)

func newJevAdminFixture(t *testing.T) (*JevAdmin, string, func()) {
	t.Helper()
	conn, _ := testutil.OpenTempSQLite(t)
	reg := jev.NewRegistry()
	tplRepo := jev.NewTemplateRepo(conn)
	decRepo := jev.NewDecisionRepo(conn)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	h := &JevAdmin{
		Registry:       reg,
		TemplateRepo:   tplRepo,
		DecisionRepo:   decRepo,
		FSTemplatesDir: "",
		DB:             conn,
		Logger:         zerolog.Nop(),
	}
	tok, err := issuer.Sign("admin", "admin")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return h, tok, func() {}
}

func jevAdminRouter(h *JevAdmin, issuer *auth.Issuer) http.Handler {
	r := chi.NewRouter()
	r.Route("/api/admin/jev", func(r chi.Router) {
		r.Use(requireAuthForTest(issuer))
		r.Get("/templates", h.ListTemplates)
		r.Get("/templates/{id}", h.GetTemplate)
		r.Post("/templates", h.CreateTemplate)
		r.Post("/templates/publish/{id}", h.PublishTemplate)
		r.Post("/templates/archive/{id}", h.ArchiveTemplate)
		r.Delete("/templates/{id}", h.DeleteTemplate)
		r.Post("/templates/reload", h.ReloadHTTP)
		r.Get("/decisions", h.ListDecisions)
		r.Get("/decisions/{id}", h.GetDecision)
		r.Post("/decisions/{id}/review", h.ReviewDecision)
		r.Get("/stats", h.Stats)
	})
	return r
}

func TestJevAdmin_ListTemplates_EmptyByDefault(t *testing.T) {
	h, tok, _ := newJevAdminFixture(t)
	router := jevAdminRouter(h, auth.NewIssuer("test-secret-32bytes-or-more-please", 0))

	rr := callJSON(router, "GET", "/api/admin/jev/templates", "", tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Total int              `json:"total"`
		Items []jev.TemplateRecord `json:"items"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 0 || len(body.Items) != 0 {
		t.Fatalf("expected empty list, got %+v", body)
	}
}

func TestJevAdmin_CreateAndGetTemplate(t *testing.T) {
	h, tok, _ := newJevAdminFixture(t)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 0)
	router := jevAdminRouter(h, issuer)

	body := jev.TemplateRecord{
		Name:         "intent_routing",
		Version:      1,
		Trigger:      jev.TriggerMessageEntry,
		OutputType:   jev.OutputChoice,
		Labels:       []string{"order", "refund"},
		Instructions: "pick one",
		Fallback:     jev.FallbackSpec{Type: "default", Value: "chitchat"},
		Status:       "draft",
	}
	raw, _ := json.Marshal(body)
	rr := callJSON(router, "POST", "/api/admin/jev/templates", string(raw), tok)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}

	// Now fetch it back.
	list := callJSON(router, "GET", "/api/admin/jev/templates", "", tok)
	var listBody struct {
		Items []jev.TemplateRecord `json:"items"`
	}
	_ = json.NewDecoder(list.Body).Decode(&listBody)
	if len(listBody.Items) != 1 {
		t.Fatalf("expected 1 row, got %d", len(listBody.Items))
	}
	id := listBody.Items[0].ID

	get := callJSON(router, "GET", "/api/admin/jev/templates/"+id, "", tok)
	if get.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", get.Code)
	}
}

func TestJevAdmin_PublishArchiveDelete(t *testing.T) {
	h, tok, _ := newJevAdminFixture(t)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 0)
	router := jevAdminRouter(h, issuer)

	body := jev.TemplateRecord{
		Name:         "intent_routing",
		Version:      1,
		Trigger:      jev.TriggerMessageEntry,
		OutputType:   jev.OutputChoice,
		Labels:       []string{"order"},
		Instructions: "x",
	}
	raw, _ := json.Marshal(body)
	create := callJSON(router, "POST", "/api/admin/jev/templates", string(raw), tok)
	var created jev.TemplateRecord
	_ = json.NewDecoder(create.Body).Decode(&created)
	id := created.ID

	pub := callJSON(router, "POST", "/api/admin/jev/templates/publish/"+id, "", tok)
	if pub.Code != http.StatusOK {
		t.Fatalf("publish expected 200, got %d", pub.Code)
	}

	arc := callJSON(router, "POST", "/api/admin/jev/templates/archive/"+id, "", tok)
	if arc.Code != http.StatusOK {
		t.Fatalf("archive expected 200, got %d", arc.Code)
	}

	del := callJSON(router, "DELETE", "/api/admin/jev/templates/"+id, "", tok)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete expected 204, got %d", del.Code)
	}
}

func TestJevAdmin_Reload_HTTP(t *testing.T) {
	h, tok, _ := newJevAdminFixture(t)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 0)
	router := jevAdminRouter(h, issuer)

	rr := callJSON(router, "POST", "/api/admin/jev/templates/reload", "", tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("reload expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestJevAdmin_ListDecisions_Empty(t *testing.T) {
	h, tok, _ := newJevAdminFixture(t)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 0)
	router := jevAdminRouter(h, issuer)
	rr := callJSON(router, "GET", "/api/admin/jev/decisions", "", tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestJevAdmin_DecisionReviewFlow(t *testing.T) {
	h, tok, _ := newJevAdminFixture(t)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 0)
	router := jevAdminRouter(h, issuer)
	ctx := context.Background()

	d := &jev.DecisionRecord{
		TenantID: tenant.DefaultID, TemplateName: "intent_routing",
		Trigger: jev.TriggerMessageEntry,
		InputHash: "h", InputJSON: "{}", OutputJSON: "{}",
	}
	if err := h.DecisionRepo.Insert(ctx, d); err != nil {
		t.Fatal(err)
	}

	body := `{"status":"accepted","groundTruth":"{\"label\":\"refund\"}"}`
	rr := callJSON(router, "POST", "/api/admin/jev/decisions/"+d.ID+"/review", body, tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("review expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	got, _ := h.DecisionRepo.Get(ctx, d.ID)
	if got.Status != "accepted" || got.ReviewedBy != "admin" {
		t.Fatalf("review didn't stick: %+v", got)
	}
}

func TestJevAdmin_Stats(t *testing.T) {
	h, tok, _ := newJevAdminFixture(t)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 0)
	router := jevAdminRouter(h, issuer)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = h.DecisionRepo.Insert(ctx, &jev.DecisionRecord{
			TenantID: tenant.DefaultID, TemplateName: "intent_routing",
			Trigger: jev.TriggerMessageEntry,
			InputHash: "h", InputJSON: "{}", OutputJSON: "{}",
		})
	}
	rr := callJSON(router, "GET", "/api/admin/jev/stats", "", tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var stats jev.Stats
	if err := json.NewDecoder(rr.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.Total != 3 {
		t.Fatalf("expected total=3, got %d", stats.Total)
	}
}

func TestJevAdmin_RequiresAuth(t *testing.T) {
	h, _, _ := newJevAdminFixture(t)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 0)
	router := jevAdminRouter(h, issuer)

	// No token → 401.
	rr := callJSON(router, "GET", "/api/admin/jev/templates", "", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", rr.Code)
	}
}

// ----- helpers --------------------------------------------------------------

func callJSON(router http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// requireAuthForTest is a tiny version of middleware.RequireAuth that
// the test can import without dragging in the middleware package.
func requireAuthForTest(issuer *auth.Issuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := r.Header.Get("Authorization")
			if len(tok) < 8 || tok[:7] != "Bearer " {
				http.Error(w, `{"code":"unauthorized","message":"missing bearer"}`, http.StatusUnauthorized)
				return
			}
			claims, err := issuer.Parse(tok[7:])
			if err != nil {
				http.Error(w, `{"code":"unauthorized","message":"bad token"}`, http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), "auth.username", claims.Username)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
