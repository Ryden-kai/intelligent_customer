// Package llm is the chat-completion facade. Both OpenAI / OpenRouter
// (which speak OpenAI's chat-completions protocol) and the Anthropic
// protocol — used by MiniMax cn — are exposed through the same ChatCompleter
// interface so the rest of the codebase does not care which one is wired in.
package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/sashabaranov/go-openai"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/log"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role    Role
	Content string
}

// ChatCompleter is what the service layer depends on. Both OpenAIClient and
// AnthropicClient satisfy it; main.go picks the concrete implementation
// based on LLM_PROVIDER.
type ChatCompleter interface {
	Chat(ctx context.Context, systemPrompt string, msgs []Message) (string, error)
	Identity() Model
}

type Model struct {
	Provider string
	Name     string
}

// ----------------------------------------------------------------------------
// OpenAI-compatible implementation (OpenAI, OpenRouter, …).
// ----------------------------------------------------------------------------

type OpenAIClient struct {
	cfg    OpenAIChannel
	client *openai.Client
	logger zerolog.Logger
}

type OpenAIChannel struct {
	BaseURL string
	Model   string
	APIKey  string
	Label   string // informational
}

func NewOpenAI(c OpenAIChannel, logger zerolog.Logger) *OpenAIClient {
	cfg := openai.DefaultConfig(c.APIKey)
	cfg.BaseURL = c.BaseURL
	return &OpenAIClient{
		cfg:    c,
		client: openai.NewClientWithConfig(cfg),
		logger: logger.With().Str("component", "llm").Str("protocol", "openai").Logger(),
	}
}

func (c *OpenAIClient) Identity() Model {
	return Model{Provider: c.cfg.Label, Name: c.cfg.Model}
}

func (c *OpenAIClient) Chat(ctx context.Context, systemPrompt string, msgs []Message) (string, error) {
	lg := log.With(ctx, c.logger)
	if c.cfg.APIKey == "" {
		lg.Error().Msg("llm_api_key_missing")
		return "", apperr.Upstream("LLM API key not configured").WithCause(errors.New("api key empty"))
	}

	openaiMsgs := make([]openai.ChatCompletionMessage, 0, len(msgs)+1)
	if systemPrompt != "" {
		openaiMsgs = append(openaiMsgs, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		})
	}
	for _, m := range msgs {
		openaiMsgs = append(openaiMsgs, openai.ChatCompletionMessage{
			Role:    strings.ToLower(string(m.Role)),
			Content: m.Content,
		})
	}

	req := openai.ChatCompletionRequest{
		Model:       c.cfg.Model,
		Messages:    openaiMsgs,
		Temperature: 0.4,
		TopP:        0.9,
	}

	lg.Debug().
		Str("model", c.cfg.Model).
		Int("messages", len(openaiMsgs)).
		Bool("has_system", systemPrompt != "").
		Msg("llm_request_start")

	start := time.Now()
	resp, err := c.client.CreateChatCompletion(ctx, req)
	if err != nil {
		lg.Error().
			Err(err).
			Int64("duration_ms", time.Since(start).Milliseconds()).
			Str("model", c.cfg.Model).
			Msg("llm_call_failed")
		return "", apperr.Upstream(fmt.Sprintf("LLM call failed: %v", err)).WithCause(err)
	}
	if len(resp.Choices) == 0 {
		lg.Error().
			Int64("duration_ms", time.Since(start).Milliseconds()).
			Msg("llm_returned_no_choices")
		return "", apperr.Upstream("LLM returned no choices")
	}

	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	lg.Info().
		Str("model", c.cfg.Model).
		Int("prompt_tokens", resp.Usage.PromptTokens).
		Int("completion_tokens", resp.Usage.CompletionTokens).
		Int("total_tokens", resp.Usage.TotalTokens).
		Int("reply_len", len(content)).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("llm_request_done")
	return content, nil
}

// ----------------------------------------------------------------------------
// Backward-compat shim
// ----------------------------------------------------------------------------

// Client is the legacy alias so service/chat.go can keep using a single
// concrete type until the switch to ChatCompleter lands everywhere.
type Client = OpenAIClient

// New keeps the older constructor signature alive. It returns an OpenAIClient;
// callers wanting the Anthropic path should use NewAnthropic directly.
func New(channel OpenAIChannel, label string, logger zerolog.Logger) *OpenAIClient {
	c := channel
	if c.Label == "" {
		c.Label = label
	}
	return NewOpenAI(c, logger)
}