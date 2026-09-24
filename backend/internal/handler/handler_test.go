package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/config"
	"intelligent_customer/backend/internal/handler"
	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/llm"
	"intelligent_customer/backend/internal/repo"
	"intelligent_customer/backend/internal/security"
	"intelligent_customer/backend/internal/service"
	"intelligent_customer/backend/internal/testutil"
)

type stubLLM struct{ content string }

func (s *stubLLM) Chat(_ context.Context, _ string, _ []llm.Message) (string, error) {
	return s.content, nil
}
func (s *stubLLM) Identity() llm.Model { return llm.Model{Provider: "stub", Name: "stub"} }

// silence unused imports
var _ = context.Background

const (
	testAdminUser = "admin"
	testAdminPass = "admin123"
)

func newServer(t *testing.T) (http.Handler, *auth.Issuer, func()) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	issuer := auth.NewIssuer("this-is-a-32-byte-development-secret-1234", 24*time.Hour)

	admins := repo.NewAdminUsers(conn)
	hash, err := security.HashPassword(testAdminPass)
	if err != nil {
		cleanup()
		t.Fatalf("hash bootstrap password: %v", err)
	}
	if _, err := admins.Insert(context.Background(), testAdminUser, hash, "admin"); err != nil {
		cleanup()
		t.Fatalf("insert bootstrap admin: %v", err)
	}

	authH := &handler.AuthHandlers{Issuer: issuer, Admins: admins, Logger: zerolog.Nop()}
	adminSvc := &service.Admin{Convs: repo.NewConversations(conn), Msgs: repo.NewMessages(conn), Feedback: repo.NewFeedback(conn), Logger: zerolog.Nop()}
	chatSvc := &service.Chat{
		Convs: repo.NewConversations(conn), Msgs: repo.NewMessages(conn), Feedbacks: repo.NewFeedback(conn),
		FAQs: repo.NewFAQs(conn), Signals: repo.NewHandoverSignals(conn),
		LLM: &stubLLM{content: "AI"}, JEV: jev.New(config.Config{}, zerolog.Nop()),
		HandoverCfg: service.HandoverConfig{ConfidenceThreshold: 0.55, SignalCountLimit: 3},
		Logger: zerolog.Nop(),
	}
	fbSvc := &service.Feedback{
		Convs: repo.NewConversations(conn), Feedback: repo.NewFeedback(conn), Msgs: repo.NewMessages(conn),
		JEV: jev.New(config.Config{}, zerolog.Nop()), Logger: zerolog.Nop(),
	}

	r := chi.NewRouter()
	r.Post("/api/chat", (&handler.Chat{Service: chatSvc, Logger: zerolog.Nop()}).ServeHTTP)
	r.Post("/api/feedback", (&handler.Feedback{Service: fbSvc, Logger: zerolog.Nop()}).ServeHTTP)
	adminH := &handler.Admin{Service: adminSvc, Logger: zerolog.Nop()}
	r.Post("/api/admin/login", authH.Login)
	r.Group(func(r chi.Router) {
		r.Use(middlewareAuth(issuer))
		r.Get("/api/admin/conversations", adminH.ListConversations)
		r.Get("/api/admin/conversations/{id}", adminH.GetConversation)
		r.Post("/api/admin/conversations/{id}/reply", adminH.PostAgentReply)
		r.Get("/api/admin/stats/satisfaction", adminH.StatsSatisfaction)
	})
	return r, issuer, cleanup
}

func TestChatHandlerSuccess(t *testing.T) {
	r, _, cleanup := newServer(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{"userId": "u1", "content": "随便聊聊"})
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["source"] == nil {
		t.Fatalf("missing source field: %s", w.Body.String())
	}
}

func TestChatHandlerBadJSON(t *testing.T) {
	r, _, cleanup := newServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", w.Code)
	}
}

func TestChatHandlerEmptyContent(t *testing.T) {
	r, _, cleanup := newServer(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{"content": "   "})
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", w.Code)
	}
}

func TestAdminLoginAndList(t *testing.T) {
	r, _, cleanup := newServer(t)
	defer cleanup()

	// Bad credentials.
	bad, _ := json.Marshal(map[string]string{"username": "x", "password": "y"})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewReader(bad))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: %d", w.Code)
	}

	// Good credentials.
	good, _ := json.Marshal(map[string]string{"username": testAdminUser, "password": testAdminPass})
	req = httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewReader(good))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", w.Code, w.Body.String())
	}
	var lr map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &lr)
	token, _ := lr["token"].(string)
	if token == "" {
		t.Fatalf("no token")
	}

	// Use token to list.
	req = httptest.NewRequest(http.MethodGet, "/api/admin/conversations", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}

	// No token = 401.
	req = httptest.NewRequest(http.MethodGet, "/api/admin/conversations", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestFeedbackHandlerValidation(t *testing.T) {
	r, _, cleanup := newServer(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]any{"rating": 7})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", w.Code)
	}
}

// helper that replicates middleware.RequireAuth without importing the package
// directly (so we can avoid the dependency cycle in this test).
func middlewareAuth(issuer *auth.Issuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, "Bearer ") {
				http.Error(w, "missing token", http.StatusUnauthorized)
				return
			}
			tok := strings.TrimPrefix(h, "Bearer ")
			c, err := issuer.Parse(tok)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), c)))
		})
	}
}

// quiet unused warnings
var _ = json.Marshal