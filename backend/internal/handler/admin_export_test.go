// Package handler — admin_export_test.go
//
// Tests for v2.2 PR5 admin conversations export + tag-batch endpoints.

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/model"
	"intelligent_customer/backend/internal/repo"
	"intelligent_customer/backend/internal/service"
)

func newAdminExportRouter(h *Admin, issuer *auth.Issuer) http.Handler {
	r := chi.NewRouter()
	r.Use(requireAuthForTest(issuer))
	r.Get("/api/admin/conversations/export", h.ExportConversations)
	r.Post("/api/admin/conversations/tag-batch", h.TagConversationsBatch)
	return r
}

func newAdminExportFixture(t *testing.T) (*Admin, string) {
	t.Helper()
	conn, _ := openTestDB(t)
	convs := repo.NewConversations(conn)
	svc := &service.Admin{Convs: convs, Logger: zerolog.Nop()}
	h := &Admin{Service: svc, Logger: zerolog.Nop()}
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	tok, _ := issuer.Sign("admin", "admin")
	return h, tok
}

func seedConversations(t *testing.T, convs *repo.Conversations, count int, statuses ...string) []string {
	t.Helper()
	ids := make([]string, 0, count)
	now := time.UnixMilli(1_700_000_000_000)
	for i := 0; i < count; i++ {
		status := model.ConvStatusOpen
		if len(statuses) > i && statuses[i] != "" {
			status = model.ConvStatus(statuses[i])
		}
		_, err := convs.DB.ExecContext(context.Background(),
			`INSERT INTO conversations(id,user_id,title,status,handed_over,created_at,updated_at)
			 VALUES(?,?,?,?,0,?,?)`,
			"conv-"+string(rune('a'+i)),
			"user-"+string(rune('a'+i)),
			"对话 "+string(rune('A'+i)),
			string(status),
			now.UnixMilli(), now.UnixMilli())
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		ids = append(ids, "conv-"+string(rune('a'+i)))
	}
	return ids
}

func openTestConn(t *testing.T) (*sqliteConn, func()) {
	t.Helper()
	conn, cleanup := openTestDB(t)
	return conn, cleanup
}// stub type so the signature compiles without dragging the full
// sqlite driver into this file's surface area. The real impl comes
// from the helper at the bottom of the file.
// (no-op; the type alias is now defined in testdb_helper_test.go)

func nowMs() int64 { return int64(1_000_000) }



// ---------------------------------------------------------------------------
// ExportConversations
// ---------------------------------------------------------------------------

func TestAdmin_ExportConversations_WritesCSVWithBOM(t *testing.T) {
	h, tok := newAdminExportFixture(t)
	seedConversations(t, h.Service.Convs, 3)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	router := newAdminExportRouter(h, issuer)

	rr := callJSON(router, "GET", "/api/admin/conversations/export", "", tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Fatalf("expected text/csv, got %q", got)
	}
	body := rr.Body.String()
	if !strings.HasPrefix(body, "\xEF\xBB\xBF") {
		t.Fatalf("expected UTF-8 BOM, got %q", body[:min(8, len(body))])
	}
	if !strings.Contains(body, "id,user_id,title,status") {
		t.Fatalf("expected header row, body=%s", body)
	}
}

func TestAdmin_ExportConversations_FilterByStatus(t *testing.T) {
	h, tok := newAdminExportFixture(t)
	seedConversations(t, h.Service.Convs, 4, "open", "closed", "open", "closed")
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	router := newAdminExportRouter(h, issuer)

	rr := callJSON(router, "GET", "/api/admin/conversations/export?status=closed", "", tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "closed") {
		t.Fatalf("expected closed rows, got %s", body)
	}
}

// ---------------------------------------------------------------------------
// TagConversationsBatch
// ---------------------------------------------------------------------------

func TestAdmin_TagConversationsBatch_AddsTag(t *testing.T) {
	h, tok := newAdminExportFixture(t)
	ids := seedConversations(t, h.Service.Convs, 3)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	router := newAdminExportRouter(h, issuer)

	body, _ := json.Marshal(map[string]any{"ids": ids[:2], "label": "priority"})
	rr := callJSON(router, "POST", "/api/admin/conversations/tag-batch", string(body), tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Succeeded int               `json:"succeeded"`
		Results   []batchItemResult `json:"results"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Succeeded != 2 {
		t.Fatalf("expected 2 succeeded, got %d", resp.Succeeded)
	}
	for _, id := range ids[:2] {
		got, err := h.Service.Convs.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got.Title, "priority") {
			t.Fatalf("expected title to contain tag, got %q", got.Title)
		}
	}
}

func TestAdmin_TagConversationsBatch_ClearRemoves(t *testing.T) {
	h, tok := newAdminExportFixture(t)
	ids := seedConversations(t, h.Service.Convs, 1)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	router := newAdminExportRouter(h, issuer)

	// First tag.
	body, _ := json.Marshal(map[string]any{"ids": ids, "label": "spam"})
	rr := callJSON(router, "POST", "/api/admin/conversations/tag-batch", string(body), tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("tag: expected 200, got %d", rr.Code)
	}
	// Then clear.
	body, _ = json.Marshal(map[string]any{"ids": ids, "clear": true})
	rr = callJSON(router, "POST", "/api/admin/conversations/tag-batch", string(body), tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear: expected 200, got %d", rr.Code)
	}
	got, _ := h.Service.Convs.Get(context.Background(), ids[0])
	if strings.Contains(got.Title, "spam") {
		t.Fatalf("tag should be cleared, got %q", got.Title)
	}
}

func TestAdmin_TagConversationsBatch_RejectsMissingLabel(t *testing.T) {
	h, tok := newAdminExportFixture(t)
	ids := seedConversations(t, h.Service.Convs, 1)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	router := newAdminExportRouter(h, issuer)

	body, _ := json.Marshal(map[string]any{"ids": ids})
	rr := callJSON(router, "POST", "/api/admin/conversations/tag-batch", string(body), tok)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing label, got %d", rr.Code)
	}
}

func TestAdmin_TagConversationsBatch_RejectsEmptyIDs(t *testing.T) {
	h, tok := newAdminExportFixture(t)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	router := newAdminExportRouter(h, issuer)

	body, _ := json.Marshal(map[string]any{"ids": []string{}, "label": "x"})
	rr := callJSON(router, "POST", "/api/admin/conversations/tag-batch", string(body), tok)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty ids, got %d", rr.Code)
	}
}

func TestAdmin_TagConversationsBatch_RejectsTooLongLabel(t *testing.T) {
	h, tok := newAdminExportFixture(t)
	ids := seedConversations(t, h.Service.Convs, 1)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	router := newAdminExportRouter(h, issuer)

	body, _ := json.Marshal(map[string]any{"ids": ids, "label": strings.Repeat("x", 65)})
	rr := callJSON(router, "POST", "/api/admin/conversations/tag-batch", string(body), tok)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for label too long, got %d", rr.Code)
	}
}

func TestAdmin_TagConversationsBatch_IdempotentOnUnknown(t *testing.T) {
	h, tok := newAdminExportFixture(t)
	issuer := auth.NewIssuer("test-secret-32bytes-or-more-please", 24*60*60*1000*1000*1000)
	router := newAdminExportRouter(h, issuer)

	body, _ := json.Marshal(map[string]any{"ids": []string{"no-such-conv"}, "label": "x"})
	rr := callJSON(router, "POST", "/api/admin/conversations/tag-batch", string(body), tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for unknown ids, got %d", rr.Code)
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
		if r.Status != "error" {
			t.Fatalf("expected error, got %+v", r)
		}
	}
}