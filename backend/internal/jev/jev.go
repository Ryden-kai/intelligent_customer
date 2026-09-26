// Package jev wraps the OpenRouter-style /alpha/decisions endpoint exposed by
// the Jev model (https://github.com/NanmiCoder/jev-arena). Jev offers three
// question types — choice, noul and score — that match our needs exactly:
//   - intent classification  -> choice over {refund, order, tech, other}
//   - handover decision      -> noul (boolean probability)
//   - satisfaction scoring   -> score over a 5-point legend
//
// We never reach into raw HTTP unless the Jev endpoint is custom-hosted, in
// which case JEV_BASE_URL points to that root.
package jev

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
	"intelligent_customer/backend/internal/config"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/model"
)

const decisionsPath = "/alpha/decisions"

type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
	logger  zerolog.Logger
}

func New(cfg config.Config, logger zerolog.Logger) *Client {
	return &Client{
		baseURL: strings.TrimRight(cfg.JEVBaseURL, "/"),
		apiKey:  cfg.JEVAPIKey,
		model:   cfg.JEVModel,
		http: &http.Client{
			Timeout: 25 * time.Second,
		},
		logger: logger.With().Str("component", "jev").Logger(),
	}
}

func (c *Client) Enabled() bool { return c.baseURL != "" && c.apiKey != "" }

// ----------------------------------------------------------------------------
// /alpha/decisions request/response shapes
// ----------------------------------------------------------------------------

type Criterion struct {
	Type         string             `json:"type"`                    // noul | choice | score
	Instructions string             `json:"instructions"`
	Criteria     map[string]any     `json:"criteria"`                // noul/choice
	Legend       []string           `json:"-"`                        // score (encoded into Criteria)
}

func (c Criterion) MarshalJSON() ([]byte, error) {
	switch c.Type {
	case "score":
		// Score criteria is an array of buckets; serialise under the same key.
		raw := struct {
			Type         string   `json:"type"`
			Instructions string   `json:"instructions"`
			Criteria     []string `json:"criteria"`
		}{c.Type, c.Instructions, c.Legend}
		return json.Marshal(raw)
	default:
		raw := struct {
			Type         string         `json:"type"`
			Instructions string         `json:"instructions"`
			Criteria     map[string]any `json:"criteria"`
		}{c.Type, c.Instructions, c.Criteria}
		return json.Marshal(raw)
	}
}

type request struct {
	Model     string              `json:"model"`
	State     string              `json:"state,omitempty"`
	Questions map[string]Criterion `json:"questions"`
}

type answerEnvelope struct {
	Type          string            `json:"type"`
	Choice        string            `json:"choice,omitempty"`
	Noul          float64           `json:"noul,omitempty"`
	Score         float64           `json:"score,omitempty"`
	Legend        map[string]any    `json:"legend,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64           `json:"confidence,omitempty"`
}

type response struct {
	Model   string                     `json:"model"`
	Answers map[string]answerEnvelope  `json:"answers"`
	Usage   map[string]any             `json:"usage,omitempty"`
	ID      string                     `json:"id,omitempty"`
}

// ----------------------------------------------------------------------------
// Public API
// ----------------------------------------------------------------------------

// ClassifyIntent returns the most likely intent label and its confidence.
// The label is one of model.Intent* constants. When Jev is disabled or the
// call fails, the error is returned so the caller can fall back to LLM.
func (c *Client) ClassifyIntent(ctx context.Context, query string, recent []string) (model.Intent, float64, error) {
	if !c.Enabled() {
		return model.IntentUnknown, 0, apperr.Upstream("jev disabled").WithCause(errors.New("missing config"))
	}

	state := buildState(recent)
	body := request{
		Model: c.model,
		State: state,
		Questions: map[string]Criterion{
			"intent": {
				Type:         "choice",
				Instructions: "判断用户当前这条消息的咨询意图，从以下四个候选中选择最匹配的一个。注意只考虑用户最新的诉求，忽略寒暄。",
				Criteria: map[string]any{
					"refund": "申请退款、退货、退款进度、退款金额、7天无理由、退款政策",
					"order":  "订单状态、发货、物流、收货地址、修改订单、查订单",
					"tech":   "App/网页/系统故障、登录、验证码、闪退、支付失败、使用问题",
					"other":  "闲聊、其他咨询、不属于以上任何一类的普通提问",
				},
			},
		},
	}
	var resp response
	if err := c.do(ctx, body, &resp); err != nil {
		return model.IntentUnknown, 0, err
	}
	a, ok := resp.Answers["intent"]
	if !ok {
		return model.IntentUnknown, 0, apperr.Upstream("jev returned no intent answer")
	}
	intent, conf, mapped := mapIntentLabel(a.Choice), a.Confidence, true
	if intent == "" {
		mapped = false
	}
	if !mapped {
		return model.IntentUnknown, a.Confidence, apperr.Upstream(fmt.Sprintf("unrecognised jev intent label: %q", a.Choice))
	}
	return intent, conf, nil
}

// NeedsHandover returns true when Jev believes this turn should escalate to
// a human agent. Confidence is the noul probability (1.0 == definitely yes).
func (c *Client) NeedsHandover(ctx context.Context, query string, recent []string) (bool, float64, error) {
	if !c.Enabled() {
		return false, 0, apperr.Upstream("jev disabled").WithCause(errors.New("missing config"))
	}

	state := buildState(recent)
	body := request{
		Model: c.model,
		State: state,
		Questions: map[string]Criterion{
			"handover": {
				Type:         "noul",
				Instructions: "判断当前是否应当把会话转接给人工客服。下列任一情况成立即为 true。",
				Criteria: map[string]any{
					"true":  "用户明确要求人工、表达强烈不满/投诉、涉及人身攻击/法律/账户安全、或问题超出 AI 能力范围、AI 已多次无法解决",
					"false": "AI 可以正常回答，或用户只是礼貌提问、寒暄",
				},
			},
		},
	}
	var resp response
	if err := c.do(ctx, body, &resp); err != nil {
		return false, 0, err
	}
	a, ok := resp.Answers["handover"]
	if !ok {
		return false, 0, apperr.Upstream("jev returned no handover answer")
	}
	return a.Noul >= 0.5, a.Noul, nil
}

// RateSatisfaction returns a 1..5 rating with confidence, derived from the
// user's free-text comment (if any). When the comment is empty it returns
// (0, 0, nil) so the caller can skip the call.
func (c *Client) RateSatisfaction(ctx context.Context, comment string) (int, float64, error) {
	if comment == "" {
		return 0, 0, nil
	}
	if !c.Enabled() {
		return 0, 0, apperr.Upstream("jev disabled").WithCause(errors.New("missing config"))
	}
	body := request{
		Model: c.model,
		Questions: map[string]Criterion{
			"satisfaction": {
				Type:         "score",
				Instructions: "根据用户的评价，给出一个 1-5 分的满意度分值，1 为非常不满意，5 为非常满意。",
				Legend:       []string{"1 非常不满意", "2 不满意", "3 一般", "4 满意", "5 非常满意"},
			},
		},
	}
	var r response
	if err := c.do(ctx, body, &r); err != nil {
		return 0, 0, err
	}
	a, ok := r.Answers["satisfaction"]
	if !ok {
		return 0, 0, apperr.Upstream("jev returned no satisfaction answer")
	}
	// score may be e.g. 3.8 — clamp to nearest bucket.
	bucket := int(a.Score + 0.5)
	if bucket < 1 {
		bucket = 1
	}
	if bucket > 5 {
		bucket = 5
	}
	return bucket, a.Confidence, nil
}

// ----------------------------------------------------------------------------
// Internals
// ----------------------------------------------------------------------------

// Decide is the v2.1 generic entry point the Orchestrator calls. It
// translates the Template + DecisionRequest into a Jev question and
// maps the response back into a Decision. Returns apperr.Upstream when
// Jev is disabled or the call fails — callers (Orchestrator) fall back
// to local rules.
//
// The mapping is deterministic per (OutputType, template.Name):
//   - choice templates → first label in tpl.Labels (or schema key) is
//     the question id; Choice + Confidence populated.
//   - score templates  → Score + Legend populated, Choice left empty.
//   - noul templates   → Noul probability returned as Confidence, Choice
//     left empty.
//
// Cost is hard-coded at $0.001 / call per Jev Arena's empirical
// numbers; revisit when we have a real OpenRouter bill.
func (c *Client) Decide(ctx context.Context, tpl *Template, req DecisionRequest) (*Decision, error) {
	if !c.Enabled() {
		return nil, apperr.Upstream("jev disabled")
	}
	state := renderInstructions(tpl.Instructions, req.Input)
	criterion := Criterion{
		Type:         string(tpl.OutputType),
		Instructions: tpl.Instructions,
	}
	switch tpl.OutputType {
	case OutputChoice:
		criterion.Criteria = map[string]any{}
		for _, l := range tpl.Labels {
			criterion.Criteria[l] = l
		}
	case OutputScore:
		criterion.Legend = tpl.Labels
	case OutputNoul:
		criterion.Criteria = map[string]any{
			"true":  "yes",
			"false": "no",
		}
	default:
		return nil, apperr.BadRequest(fmt.Sprintf("unsupported output_type %q", tpl.OutputType))
	}
	body := request{
		Model: c.model,
		State: state,
		Questions: map[string]Criterion{
			"q": criterion,
		},
	}
	var resp response
	if err := c.do(ctx, body, &resp); err != nil {
		return nil, err
	}
	ans, ok := resp.Answers["q"]
	if !ok {
		return nil, apperr.Upstream("jev returned no answer")
	}
	d := &Decision{
		TenantID:        req.TenantID,
		TemplateName:    tpl.Name,
		TemplateVersion: tpl.Version,
		Trigger:         req.Trigger,
		OutputType:      tpl.OutputType,
		CostUSD:         0.001,
	}
	switch tpl.OutputType {
	case OutputChoice:
		d.Choice = ans.Choice
		d.Scores = map[string]any{"confidence": ans.Confidence}
		conf := ans.Confidence
		d.Confidence = &conf
	case OutputScore:
		d.Scores = map[string]any{"score": ans.Score}
	case OutputNoul:
		conf := ans.Noul
		d.Confidence = &conf
		d.Scores = map[string]any{"noul": ans.Noul}
	}
	return d, nil
}

// renderInstructions produces the "state" field for Jev (the contextual
// prelude to the criterion). v2.1 uses a simple template substitution
// via the {{.key}} form defined by Go's text/template — kept here
// instead of importing text/template because we only support the dot
// key=value shape used by the YAML files.
func renderInstructions(body string, input map[string]any) string {
	if body == "" || len(input) == 0 {
		return ""
	}
	out := body
	for k, v := range input {
		token := fmt.Sprintf("{{.%s}}", k)
		out = strings.ReplaceAll(out, token, fmt.Sprintf("%v", v))
	}
	// Strip any remaining placeholders so Jev doesn't see literal {{...}}.
	for {
		start := strings.Index(out, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(out[start:], "}}")
		if end < 0 {
			break
		}
		out = out[:start] + out[start+end+2:]
	}
	return out
}

func (c *Client) do(ctx context.Context, body request, out any) error {
	lg := log.With(ctx, c.logger)
	start := time.Now()

	// Log the question ids we are sending so a glance at the log tells you
	// which call path was taken without parsing the JSON.
	qids := make([]string, 0, len(body.Questions))
	for k := range body.Questions {
		qids = append(qids, k)
	}
	lg.Debug().
		Str("model", body.Model).
		Strs("questions", qids).
		Msg("jev_request_start")

	raw, err := json.Marshal(body)
	if err != nil {
		lg.Error().Err(err).Msg("jev_marshal_failed")
		return apperr.Internal("marshal jev request").WithCause(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+decisionsPath, bytes.NewReader(raw))
	if err != nil {
		lg.Error().Err(err).Msg("jev_build_request_failed")
		return apperr.Internal("build jev request").WithCause(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		lg.Error().Err(err).Int64("duration_ms", time.Since(start).Milliseconds()).Msg("jev_transport_error")
		return apperr.Upstream("jev transport error").WithCause(err)
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		lg.Error().Err(err).Msg("jev_body_read_failed")
		return apperr.Upstream("jev body read").WithCause(err)
	}
	if resp.StatusCode >= 400 {
		lg.Error().
			Int("status", resp.StatusCode).
			Int("bytes", len(buf)).
			Str("body", truncate(string(buf), 512)).
			Int64("duration_ms", time.Since(start).Milliseconds()).
			Msg("jev_http_error")
		return apperr.Upstream(fmt.Sprintf("jev returned %d: %s", resp.StatusCode, strings.TrimSpace(string(buf))))
	}
	if err := json.Unmarshal(buf, out); err != nil {
		lg.Error().
			Err(err).
			Int("status", resp.StatusCode).
			Int("bytes", len(buf)).
			Str("body", truncate(string(buf), 512)).
			Msg("jev_bad_json")
		return apperr.Upstream("jev bad json").WithCause(err)
	}

	lg.Info().
		Int("status", resp.StatusCode).
		Int("bytes", len(buf)).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Strs("questions", qids).
		Msg("jev_request_done")
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

func buildState(recent []string) string {
	if len(recent) == 0 {
		return ""
	}
	const max = 6
	if len(recent) > max {
		recent = recent[len(recent)-max:]
	}
	var sb strings.Builder
	sb.WriteString("最近对话：\n")
	for i, m := range recent {
		fmt.Fprintf(&sb, "[%d] %s\n", i+1, m)
	}
	return sb.String()
}

func mapIntentLabel(s string) model.Intent {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "refund":
		return model.IntentRefund
	case "order":
		return model.IntentOrder
	case "tech":
		return model.IntentTech
	case "other":
		return model.IntentOther
	default:
		return ""
	}
}