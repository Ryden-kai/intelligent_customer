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

// mockOpenAIServer is a minimal OpenAI-compatible chat/completions mock.
func mockOpenAIServer(t *testing.T, content string) (*httptest.Server, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Fatalf("bad path: %s", r.URL.Path)
		}
		// Echo the model so we can verify it was forwarded correctly.
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "test",
			"model": body["model"],
			"choices": []map[string]any{
				{"index": 0, "message": map[string]any{"role": "assistant", "content": content}},
			},
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 7, "total_tokens": 12},
		})
	}))
	return srv, srv.Close
}

func TestOpenAIChat(t *testing.T) {
	srv, cleanup := mockOpenAIServer(t, "你好，有什么可以帮你？")
	defer cleanup()
	c := NewOpenAI(OpenAIChannel{
		BaseURL: srv.URL,
		Model:   "test-model",
		APIKey:  "sk-test",
		Label:   "test",
	}, zerolog.Nop())

	out, err := c.Chat(context.Background(), "你是一个助手", []Message{
		{Role: RoleUser, Content: "你好"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !strings.Contains(out, "你好") {
		t.Fatalf("reply mismatch: %q", out)
	}
	if c.Identity().Name != "test-model" {
		t.Fatalf("identity: %+v", c.Identity())
	}
}

func TestOpenAIChatRejectsMissingKey(t *testing.T) {
	c := NewOpenAI(OpenAIChannel{
		BaseURL: "http://localhost",
		Model:   "x",
		APIKey:  "",
	}, zerolog.Nop())
	if _, err := c.Chat(context.Background(), "", nil); err == nil {
		t.Fatalf("expected error on missing key")
	}
}

func TestOpenAIChatHandlesBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit"}}`))
	}))
	defer srv.Close()
	c := NewOpenAI(OpenAIChannel{
		BaseURL: srv.URL,
		Model:   "x",
		APIKey:  "sk-test",
	}, zerolog.Nop())
	_, err := c.Chat(context.Background(), "", nil)
	if err == nil {
		t.Fatalf("expected error on 429")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("err should mention status: %v", err)
	}
}