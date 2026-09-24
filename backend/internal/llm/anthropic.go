// Anthropic-protocol client. Used by MiniMax cn (platform.minimaxi.cn) per
// the official quickstart (https://platform.minimaxi.cn/docs/token-plan/quickstart):
// the platform exposes an Anthropic-compatible endpoint at
//
//	{ANTHROPIC_BASE_URL}/v1/messages
//
// Messages follow the Anthropic Messages shape — `system` is a top-level
// field (not a message), message content is `string`, and the response's
// first `text` content block is returned.
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
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	System    string           `json:"system,omitempty"`
	Messages  []anthropicMsg   `json:"messages"`
}

type anthropicMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messagesResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	StopReason string `json:"stop_reason"`
	Content []struct {
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
	lg := log.With(ctx, c.logger)
	if c.apiKey == "" {
		lg.Error().Msg("llm_api_key_missing")
		return "", apperr.Upstream("LLM API key not configured").WithCause(errors.New("api key empty"))
	}

	anthMsgs := make([]anthropicMsg, 0, len(msgs))
	for _, m := range msgs {
		role := strings.ToLower(string(m.Role))
		if role == "system" {
			// System belongs in the top-level system field; append to it.
			if systemPrompt != "" {
				systemPrompt += "\n\n"
			}
			systemPrompt += m.Content
			continue
		}
		if role == "assistant" {
			anthMsgs = append(anthMsgs, anthropicMsg{Role: "assistant", Content: m.Content})
			continue
		}
		anthMsgs = append(anthMsgs, anthropicMsg{Role: "user", Content: m.Content})
	}

	body := messagesRequest{
		Model:     c.model,
		MaxTokens: 1024,
		System:    systemPrompt,
		Messages:  anthMsgs,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", apperr.Internal("marshal anthropic request").WithCause(err)
	}

	url := c.baseURL + "/v1/messages"
	lg.Debug().
		Str("model", c.model).
		Int("messages", len(anthMsgs)).
		Bool("has_system", systemPrompt != "").
		Str("url", url).
		Msg("llm_request_start")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", apperr.Internal("build anthropic request").WithCause(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	// Anthropic also accepts the Authorization header form; we set both.
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