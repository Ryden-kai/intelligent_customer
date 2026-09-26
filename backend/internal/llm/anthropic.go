// Anthropic-protocol client. Used by MiniMax cn (platform.minimaxi.cn) per
// the official quickstart (https://platform.minimaxi.cn/docs/token-plan/quickstart):
// the platform exposes an Anthropic-compatible endpoint at
//
//	{ANTHROPIC_BASE_URL}/v1/messages
//
// Messages follow the Anthropic Messages shape — `system` is a top-level
// field (not a message), message content is `string`, and the response's
// first `text` content block is returned.
//
// Tool calling: MiniMax cn does not (yet) expose the native tool_use API,
// so we use a JSON-instruction fallback. The system prompt carries the
// tool list and the model is told to either:
//   - emit a tool call: `{"tool_calls":[{"id":"…","name":"…","arguments":{…}}]}`
//   - or reply to the user directly with a plain string
// The parser in tool_fallback.go converts the reply back into a
// ToolChatResult so the agent loop is protocol-agnostic.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/log"
)

type AnthropicClient struct {
	baseURL string
	model   string
	apiKey  string
	label   string
	http    *http.Client
	logger  zerolog.Logger
}

type AnthropicChannel struct {
	BaseURL string
	Model   string
	APIKey  string
	Label   string
}

func NewAnthropic(c AnthropicChannel, logger zerolog.Logger) *AnthropicClient {
	return &AnthropicClient{
		baseURL: strings.TrimRight(c.BaseURL, "/"),
		model:   c.Model,
		apiKey:  c.APIKey,
		label:   c.Label,
		http:    &http.Client{Timeout: 60 * time.Second},
		logger:  logger.With().Str("component", "llm").Str("protocol", "anthropic").Logger(),
	}
}

func (c *AnthropicClient) Identity() Model {
	return Model{Provider: c.label, Name: c.model}
}

// messagesRequest mirrors the Anthropic Messages API request shape.
type messagesRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system,omitempty"`
	Messages  []anthropicMsg `json:"messages"`
}

type anthropicMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messagesResponse struct {
	ID         string `json:"id"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *AnthropicClient) Chat(ctx context.Context, systemPrompt string, msgs []Message) (string, error) {
	body, err := c.buildRequest(systemPrompt, msgs, nil)
	if err != nil {
		return "", err
	}
	return c.doChat(ctx, body)
}

func (c *AnthropicClient) ToolChat(ctx context.Context, systemPrompt string, msgs []Message, tools []ToolDef) (ToolChatResult, error) {
	enriched := injectToolInstructions(systemPrompt, tools)
	body, err := c.buildRequest(enriched, msgs, nil)
	if err != nil {
		return ToolChatResult{}, err
	}
	raw, err := c.doChatRaw(ctx, body)
	if err != nil {
		return ToolChatResult{}, err
	}
	return parseToolFallback(raw), nil
}

// buildRequest converts the chat inputs into the Anthropic Messages shape.
// Tool messages are flattened into user-side text so the model still sees
// the tool result in conversational order — important because Anthropic
// has no native "tool" role.
func (c *AnthropicClient) buildRequest(systemPrompt string, msgs []Message, _ []ToolDef) (messagesRequest, error) {
	anthMsgs := make([]anthropicMsg, 0, len(msgs))
	for _, m := range msgs {
		role := strings.ToLower(string(m.Role))
		switch role {
		case "system":
			if systemPrompt != "" {
				systemPrompt += "\n\n"
			}
			systemPrompt += m.Content
		case "assistant":
			text := m.Content
			if len(m.ToolCalls) > 0 {
				// Echo the tool calls as text so the next user turn has the
				// decision context (Anthropic protocol can't carry tool_calls
				// as a structured field in this fallback).
				b, _ := json.Marshal(m.ToolCalls)
				text = text + "\n" + string(b)
			}
			anthMsgs = append(anthMsgs, anthropicMsg{Role: "assistant", Content: text})
		case "tool":
			anthMsgs = append(anthMsgs, anthropicMsg{
				Role:    "user",
				Content: fmt.Sprintf("[tool_result for %s] %s", m.ToolName, m.Content),
			})
		default:
			anthMsgs = append(anthMsgs, anthropicMsg{Role: "user", Content: m.Content})
		}
	}
	return messagesRequest{
		Model:     c.model,
		MaxTokens: 1024,
		System:    systemPrompt,
		Messages:  anthMsgs,
	}, nil
}

func (c *AnthropicClient) doChat(ctx context.Context, body messagesRequest) (string, error) {
	raw, err := c.doChatRaw(ctx, body)
	if err != nil {
		return "", err
	}
	if raw == "" {
		return "", apperr.Upstream("anthropic returned no text block")
	}
	return raw, nil
}

// doChatRaw does the HTTP call and concatenates every text block in the
// response. The tool_fallback parser (parseToolFallback) is what turns
// the raw text into a ToolChatResult.
func (c *AnthropicClient) doChatRaw(ctx context.Context, body messagesRequest) (string, error) {
	lg := log.With(ctx, c.logger)
	if c.apiKey == "" {
		lg.Error().Msg("llm_api_key_missing")
		return "", apperr.Upstream("LLM API key not configured").WithCause(errors.New("api key empty"))
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", apperr.Internal("marshal anthropic request").WithCause(err)
	}
	url := c.baseURL + "/v1/messages"
	lg.Debug().
		Str("model", c.model).
		Int("messages", len(body.Messages)).
		Bool("has_system", body.System != "").
		Str("url", url).
		Msg("llm_request_start")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", apperr.Internal("build anthropic request").WithCause(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		lg.Error().Err(err).Int64("duration_ms", time.Since(start).Milliseconds()).Msg("llm_call_failed")
		return "", apperr.Upstream("anthropic transport error").WithCause(err)
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", apperr.Upstream("anthropic body read").WithCause(err)
	}

	var mr messagesResponse
	if err := json.Unmarshal(buf, &mr); err != nil {
		lg.Error().
			Err(err).
			Int("status", resp.StatusCode).
			Int("bytes", len(buf)).
			Str("body", truncateAnthropic(string(buf), 512)).
			Msg("llm_bad_json")
		return "", apperr.Upstream("anthropic bad json").WithCause(err)
	}
	if resp.StatusCode >= 400 || mr.Error != nil {
		msg := fmt.Sprintf("anthropic returned %d", resp.StatusCode)
		if mr.Error != nil {
			msg = fmt.Sprintf("anthropic error (%s): %s", mr.Error.Type, mr.Error.Message)
		}
		lg.Error().
			Int("status", resp.StatusCode).
			Int("bytes", len(buf)).
			Str("body", truncateAnthropic(string(buf), 512)).
			Int64("duration_ms", time.Since(start).Milliseconds()).
			Msg("llm_http_error")
		return "", apperr.Upstream(msg)
	}

	var text strings.Builder
	for _, b := range mr.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	content := strings.TrimSpace(text.String())
	if content == "" {
		lg.Error().
			Int64("duration_ms", time.Since(start).Milliseconds()).
			Msg("llm_returned_no_text_block")
		return "", apperr.Upstream("anthropic returned no text block")
	}

	lg.Info().
		Str("model", c.model).
		Int("input_tokens", mr.Usage.InputTokens).
		Int("output_tokens", mr.Usage.OutputTokens).
		Int("reply_len", len(content)).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Str("stop_reason", mr.StopReason).
		Msg("llm_request_done")
	return content, nil
}

func truncateAnthropic(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}