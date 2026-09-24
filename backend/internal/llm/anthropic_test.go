package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func mockAnthropicServer(t *testing.T, content string) (*httptest.Server, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/messages") {
			t.Fatalf("bad path: %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") == "" && r.Header.Get("Authorization") == "" {
			t.Fatalf("missing api key header")
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Fatalf("missing anthropic-version header")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["max_tokens"]; !ok {
			t.Fatalf("max_tokens missing")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_test",
			"model":       body["model"],
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": content}},
			"usage":       map[string]any{"input_tokens": 8, "output_tokens": 6},
		})
	}))
	return srv, srv.Close
}

func TestAnthropicChat(t *testing.T) {
	srv, cleanup := mockAnthropicServer(t, "Hello back")
	defer cleanup()
	c := NewAnthropic(AnthropicChannel{
		BaseURL: srv.URL,
		Model:   "MiniMax-test",
		APIKey:  "sk-test",
		Label:   "MiniMax",
	}, zerolog.Nop())
	out, err := c.Chat(context.Background(), "be brief", []Message{
		{Role: RoleUser, Content: "hi"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if out != "Hello back" {
		t.Fatalf("reply: %q", out)
	}
	if c.Identity().Name != "MiniMax-test" {
		t.Fatalf("identity: %+v", c.Identity())
	}
}

func TestAnthropicChatRejectsMissingKey(t *testing.T) {
	c := NewAnthropic(AnthropicChannel{
		BaseURL: "http://localhost",
		Model:   "x",
		APIKey:  "",
	}, zerolog.Nop())
	if _, err := c.Chat(context.Background(), "", nil); err == nil {
		t.Fatalf("expected error")
	}
}

func TestAnthropicChatHandlesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"auth_error","message":"bad key"}}`))
	}))
	defer srv.Close()
	c := NewAnthropic(AnthropicChannel{
		BaseURL: srv.URL,
		Model:   "x",
		APIKey:  "sk-test",
	}, zerolog.Nop())
	_, err := c.Chat(context.Background(), "", nil)
	if err == nil || !strings.Contains(err.Error(), "auth_error") {
		t.Fatalf("expected auth_error in err, got %v", err)
	}
}

func TestAnthropicChatMergesSystemMessages(t *testing.T) {
	captured := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if sys, ok := body["system"].(string); ok {
			captured = sys
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()
	c := NewAnthropic(AnthropicChannel{
		BaseURL: srv.URL,
		Model:   "x",
		APIKey:  "sk-test",
	}, zerolog.Nop())
	_, _ = c.Chat(context.Background(), "first", []Message{
		{Role: RoleSystem, Content: "second"},
		{Role: RoleUser, Content: "third"},
	})
	if !strings.Contains(captured, "first") || !strings.Contains(captured, "second") {
		t.Fatalf("system field should merge top-level + system messages, got %q", captured)
	}
}