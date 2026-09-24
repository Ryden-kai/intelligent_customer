package service_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/config"
	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/llm"
	"intelligent_customer/backend/internal/model"
	"intelligent_customer/backend/internal/repo"
	"intelligent_customer/backend/internal/service"
	"intelligent_customer/backend/internal/testutil"
)

// stubLLM is a deterministic ChatCompleter for service tests.
type stubLLM struct {
	content string
	err     error
	calls   int
}

func (s *stubLLM) Chat(_ context.Context, _ string, _ []llm.Message) (string, error) {
	s.calls++
	return s.content, s.err
}
func (s *stubLLM) Identity() llm.Model { return llm.Model{Provider: "stub", Name: "stub"} }

// newJevStub returns a jev.Client whose transport hits a tiny httptest server
// returning the supplied answer JSON.
func newJevStub(t *testing.T, body string) *jev.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return jev.New(config.Config{
		JEVBaseURL: srv.URL,
		JEVAPIKey:  "sk-test",
		JEVModel:   "stub/jev",
	}, zerolog.Nop())
}

func newServiceWithDB(t *testing.T, conn *sql.DB, llmImpl llm.ChatCompleter, jevC *jev.Client) *service.Chat {
	t.Helper()
	chat := &service.Chat{
		Convs:     repo.NewConversations(conn),
		Msgs:      repo.NewMessages(conn),
		Feedbacks: repo.NewFeedback(conn),
		FAQs:      repo.NewFAQs(conn),
		Signals:   repo.NewHandoverSignals(conn),
		LLM:       llmImpl,
		JEV:       jevC,
		HandoverCfg: service.HandoverConfig{
			ConfidenceThreshold: 0.55,
			SignalCountLimit:    3,
		},
		Logger: testutil.QuietLogger(),
	}
	return chat
}

func newService(t *testing.T, llmImpl llm.ChatCompleter, jevC *jev.Client) (*service.Chat, func()) {
	t.Helper()
	conn, cleanup := testutil.OpenTempSQLite(t)
	return newServiceWithDB(t, conn, llmImpl, jevC), cleanup
}

func TestChatFAQHit(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	// Seed one FAQ so the score-based matcher has something to find.
	if _, err := conn.Exec(`INSERT INTO faqs(id,category,question,answer,keywords,enabled,created_at) VALUES(?,?,?,?,?,1,?)`,
		"faq-1", "refund", "如何申请退款", "在订单页点申请退款", "退款,申请", int64(1)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	chat := newServiceWithDB(t, conn,
		&stubLLM{content: "should not be called"},
		jev.New(config.Config{}, zerolog.Nop())) // disabled

	resp, err := chat.Handle(context.Background(), service.ChatRequest{
		UserID: "u1", Content: "我想申请退款",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if resp.Source != "faq" {
		t.Fatalf("source: %q", resp.Source)
	}
	if !strings.Contains(resp.Message.Content, "申请退款") {
		t.Fatalf("reply: %q", resp.Message.Content)
	}
	if resp.HandedOver {
		t.Fatalf("should not have handed over")
	}
}

func TestChatFallsBackToLLM(t *testing.T) {
	chat, cleanup := newService(t,
		&stubLLM{content: "AI 回复：您好"},
		jev.New(config.Config{}, zerolog.Nop())) // disabled
	defer cleanup()

	resp, err := chat.Handle(context.Background(), service.ChatRequest{
		UserID: "u1", Content: "随便聊聊",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if resp.Source != "stub:stub" {
		t.Fatalf("source: %q", resp.Source)
	}
	if resp.Message.Content != "AI 回复：您好" {
		t.Fatalf("reply: %q", resp.Message.Content)
	}
}

func TestChatJevClassifies(t *testing.T) {
	ans, _ := json.Marshal(map[string]any{
		"answers": map[string]any{
			"intent": map[string]any{"type": "choice", "choice": "tech", "confidence": 0.88},
		},
	})
	chat, cleanup := newService(t,
		&stubLLM{content: "AI tech reply"},
		newJevStub(t, string(ans)))
	defer cleanup()

	resp, err := chat.Handle(context.Background(), service.ChatRequest{
		UserID: "u1", Content: "App 闪退了",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if resp.Intent != model.IntentTech {
		t.Fatalf("intent: %q", resp.Intent)
	}
	if resp.IntentConfidence < 0.8 {
		t.Fatalf("confidence: %f", resp.IntentConfidence)
	}
}

func TestChatHandoverOnUserRequest(t *testing.T) {
	chat, cleanup := newService(t,
		&stubLLM{content: "好的"},
		jev.New(config.Config{}, zerolog.Nop()))
	defer cleanup()

	resp, err := chat.Handle(context.Background(), service.ChatRequest{
		UserID: "u1", Content: "请帮我转人工，谢谢",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !resp.HandedOver {
		t.Fatalf("expected handed over")
	}
	if !strings.Contains(resp.Message.Content, "人工") {
		t.Fatalf("reply should mention handover: %q", resp.Message.Content)
	}
}

func TestChatRequiresContent(t *testing.T) {
	chat, cleanup := newService(t, &stubLLM{}, jev.New(config.Config{}, zerolog.Nop()))
	defer cleanup()
	_, err := chat.Handle(context.Background(), service.ChatRequest{
		UserID: "u1", Content: "   ",
	})
	if err == nil {
		t.Fatalf("expected error on empty content")
	}
}

func TestChatAppendsToExistingConversation(t *testing.T) {
	chat, cleanup := newService(t,
		&stubLLM{content: "next"},
		jev.New(config.Config{}, zerolog.Nop()))
	defer cleanup()
	first, err := chat.Handle(context.Background(), service.ChatRequest{
		UserID: "u1", Content: "你好",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := chat.Handle(context.Background(), service.ChatRequest{
		UserID: "u1", ConversationID: first.ConversationID, Content: "再聊",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ConversationID != second.ConversationID {
		t.Fatalf("conv id mismatch")
	}
}