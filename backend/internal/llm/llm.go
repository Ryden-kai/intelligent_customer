// Package llm is the chat-completion facade. Both OpenAI / OpenRouter
// (which speak OpenAI's chat-completions protocol) and the Anthropic
// protocol — used by MiniMax cn — are exposed through the same ChatCompleter
// interface so the rest of the codebase does not care which one is wired in.
//
// On top of that, this package exposes a ToolChatCompleter interface for
// Agent-style tool calling. OpenAIClient implements it natively (using
// openai.ChatCompletionRequest.Tools); AnthropicClient implements it
// using a JSON-instruction fallback so MiniMax cn can drive the agent
// even without native tool_use support.
package llm

import (
	"context"
	"encoding/json"
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
	RoleTool      Role = "tool"
)

// Message is the wire-shape both protocols accept. RoleTool is only used
// by the Agent loop to feed a tool result back to the LLM.
type Message struct {
	Role    Role
	Content string

	// ToolCalls is populated by the LLM (assistant turn) and represents
	// one or more parallel tool invocations.
	ToolCalls []ToolCall

	// ToolCallID and ToolName must be set on RoleTool messages so the LLM
	// knows which tool result maps to which call.
	ToolCallID string
	ToolName   string
}

// ToolCall is the LLM's request to invoke a tool. The Agent decides how
// to fulfil it (registry lookup → Execute).
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON string
}

// ToolDef is what we tell the LLM exists. Independent of the openai lib
// so the agent package can build these without importing it.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema for arguments
}

// ChatCompleter is the text-only path. Both OpenAIClient and AnthropicClient
// satisfy it; main.go picks the concrete implementation based on LLM_PROVIDER.
type ChatCompleter interface {
	Chat(ctx context.Context, systemPrompt string, msgs []Message) (string, error)
	Identity() Model
}

// ToolChatCompleter is the Agent path. It returns the assistant content
// AND any tool calls the LLM decided to make. Implementations:
//
//   - OpenAIClient.ToolChat uses openai.ChatCompletionRequest.Tools natively.
//   - AnthropicClient.ToolChat injects the tool schema into the system
//     prompt and parses JSON from the response (see tool_fallback.go).
//
// Identity() is duplicated here so the agent can log provider+model on
// every step.
type ToolChatCompleter interface {
	ToolChat(ctx context.Context, systemPrompt string, msgs []Message, tools []ToolDef) (ToolChatResult, error)
	Identity() Model
}

// ToolChatResult is what ToolChatCompleter returns. Content holds the
// assistant's text reply (may be empty when only tool_calls). ToolCalls
// is non-empty only when the LLM decided to invoke at least one tool.
type ToolChatResult struct {
	Content   string
	ToolCalls []ToolCall
	Usage     TokenUsage
}

// TokenUsage is forwarded into the audit log so cost control has a handle.
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
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

// ToolChat implements ToolChatCompleter for OpenAIClient using the native
// openai.ChatCompletionRequest.Tools field. The response's
// Choice.Message.ToolCalls is mapped into []ToolCall.
func (c *OpenAIClient) ToolChat(ctx context.Context, systemPrompt string, msgs []Message, tools []ToolDef) (ToolChatResult, error) {
	lg := log.With(ctx, c.logger)
	if c.cfg.APIKey == "" {
		lg.Error().Msg("llm_api_key_missing")
		return ToolChatResult{}, apperr.Upstream("LLM API key not configured").WithCause(errors.New("api key empty"))
	}

	openaiMsgs := make([]openai.ChatCompletionMessage, 0, len(msgs)+1)
	if systemPrompt != "" {
		openaiMsgs = append(openaiMsgs, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		})
	}
	for _, m := range msgs {
		role := strings.ToLower(string(m.Role))
		ocm := openai.ChatCompletionMessage{Role: role}
		switch role {
		case "tool":
			// Tool result message: content + tool_call_id.
			ocm.Content = m.Content
			if m.ToolCallID != "" {
				ocm.ToolCallID = m.ToolCallID
			}
		case "assistant":
			ocm.Content = m.Content
			if len(m.ToolCalls) > 0 {
				for _, tc := range m.ToolCalls {
					ocm.ToolCalls = append(ocm.ToolCalls, openai.ToolCall{
						ID:   tc.ID,
						Type: openai.ToolTypeFunction,
						Function: openai.FunctionCall{
							Name:      tc.Name,
							Arguments: tc.Arguments,
						},
					})
				}
			}
		default:
			ocm.Content = m.Content
		}
		openaiMsgs = append(openaiMsgs, ocm)
	}

	openaiTools := make([]openai.Tool, 0, len(tools))
	for _, t := range tools {
		params := string(t.Parameters)
		if params == "" {
			params = `{"type":"object","properties":{}}`
		}
		openaiTools = append(openaiTools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  json.RawMessage(params),
			},
		})
	}

	req := openai.ChatCompletionRequest{
		Model:       c.cfg.Model,
		Messages:    openaiMsgs,
		Tools:       openaiTools,
		Temperature: 0.4,
		TopP:        0.9,
	}

	lg.Debug().
		Str("model", c.cfg.Model).
		Int("messages", len(openaiMsgs)).
		Int("tools", len(openaiTools)).
		Bool("has_system", systemPrompt != "").
		Msg("llm_tool_request_start")

	start := time.Now()
	resp, err := c.client.CreateChatCompletion(ctx, req)
	if err != nil {
		lg.Error().
			Err(err).
			Int64("duration_ms", time.Since(start).Milliseconds()).
			Str("model", c.cfg.Model).
			Msg("llm_tool_call_failed")
		return ToolChatResult{}, apperr.Upstream(fmt.Sprintf("LLM call failed: %v", err)).WithCause(err)
	}
	if len(resp.Choices) == 0 {
		lg.Error().
			Int64("duration_ms", time.Since(start).Milliseconds()).
			Msg("llm_tool_returned_no_choices")
		return ToolChatResult{}, apperr.Upstream("LLM returned no choices")
	}

	msg := resp.Choices[0].Message
	result := ToolChatResult{
		Content: strings.TrimSpace(msg.Content),
		Usage: TokenUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}
	for _, tc := range msg.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	lg.Info().
		Str("model", c.cfg.Model).
		Int("prompt_tokens", resp.Usage.PromptTokens).
		Int("completion_tokens", resp.Usage.CompletionTokens).
		Int("total_tokens", resp.Usage.TotalTokens).
		Int("tool_calls", len(result.ToolCalls)).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("llm_tool_request_done")
	return result, nil
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