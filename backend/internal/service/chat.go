// Package service contains the business logic of the customer-service
// application. Handlers translate HTTP into service calls and back; service
// never imports HTTP types, so it stays unit-testable. Every step of a
// conversation emits a structured log so production incidents are easy to
// trace end-to-end.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/llm"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/model"
	"intelligent_customer/backend/internal/repo"
)

const maxHistoryForLLM = 12

type Chat struct {
	Convs       *repo.Conversations
	Msgs        *repo.Messages
	Feedbacks   *repo.Feedback
	FAQs        *repo.FAQs
	Signals     *repo.HandoverSignals
	LLM         llm.ChatCompleter
	JEV         *jev.Client
	HandoverCfg HandoverConfig
	Logger      zerolog.Logger
}

// HandoverConfig holds tuning knobs read from config.
type HandoverConfig struct {
	ConfidenceThreshold float64 // if intent confidence < threshold, signal a handover
	SignalCountLimit    int     // signal count >= limit triggers handover
}

// ----------------------------------------------------------------------------
// Inbound: ChatRequest
// ----------------------------------------------------------------------------

type ChatRequest struct {
	ConversationID string
	UserID         string
	Content        string
	// FirstUserMessage is informational only — handled implicitly via empty conv id.
}

type ChatResponse struct {
	ConversationID   string           `json:"conversationId"`
	Message          ChatMessage      `json:"message"`
	Intent           model.Intent     `json:"intent"`
	IntentConfidence float64          `json:"intentConfidence"`
	HandedOver       bool             `json:"handedOver"`
	Source           string           `json:"source"` // faq | llm | handover
	UserMessage      ChatMessage      `json:"userMessage"`
}

type ChatMessage struct {
	ID        string            `json:"id"`
	Role      model.MessageRole `json:"role"`
	Content   string            `json:"content"`
	Model     string            `json:"model,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
}

// Handle is the main entry point used by the HTTP handler. It persists the
// user message, classifies intent (via Jev), retrieves a FAQ if applicable,
// otherwise asks the LLM, and finally decides whether to hand over to a
// human agent. Every meaningful step emits a log line tagged with the
// request id so the lifecycle is debuggable end-to-end.
func (s *Chat) Handle(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	lg := log.With(ctx, s.Logger)

	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, apperr.BadRequest("message content required")
	}
	if req.UserID == "" {
		req.UserID = "anon-" + uuid.NewString()
	}

	lg.Info().
		Str("user_id", req.UserID).
		Str("conv_id", req.ConversationID).
		Int("content_len", len(content)).
		Msg("chat_request_received")

	conv, err := s.resolveConversation(ctx, req)
	if err != nil {
		return nil, err
	}
	lg.Debug().Str("conv_id", conv.ID).Str("status", string(conv.Status)).Msg("conversation_resolved")

	// 2. Persist the user message first so we have a record even if downstream
	// services fail.
	userMsg := &model.Message{
		ConversationID: conv.ID,
		Role:           model.RoleUser,
		Content:        content,
	}
	if err := s.Msgs.Insert(ctx, userMsg); err != nil {
		lg.Error().Err(err).Str("conv_id", conv.ID).Msg("persist_user_message_failed")
		return nil, apperr.Internal("persist user message").WithCause(err)
	}
	if err := s.Convs.Touch(ctx, conv.ID); err != nil {
		lg.Warn().Err(err).Str("conv_id", conv.ID).Msg("touch_conv_failed")
	}

	// 3. Load recent history for context.
	history, err := s.Msgs.LastN(ctx, conv.ID, maxHistoryForLLM)
	if err != nil {
		lg.Error().Err(err).Msg("load_history_failed")
		return nil, apperr.Internal("load history").WithCause(err)
	}
	lg.Debug().Int("history_len", len(history)).Msg("history_loaded")

	// 4. Classify intent (Jev, falls back to LLM).
	intent, conf, source, err := s.classifyIntent(ctx, content, history)
	if err != nil {
		// Non-fatal: continue with unknown intent.
		lg.Warn().Err(err).Msg("intent_classify_degraded")
		intent, conf, source = model.IntentUnknown, 0, "fallback"
	}
	lg.Info().
		Str("intent", string(intent)).
		Float64("confidence", conf).
		Str("source", string(source)).
		Msg("intent_classified")

	// Persist detected intent on the user message.
	cf := conf
	userMsg.Intent = intent
	userMsg.IntentConfidence = &cf
	userMsg.Model = string(source)
	if _, err := s.Msgs.DBConn().ExecContext(ctx,
		`UPDATE messages SET intent=?, intent_confidence=?, model=? WHERE id=?`,
		nullStr(string(intent)), cf, string(source), userMsg.ID); err != nil {
		lg.Warn().Err(err).Msg("update_intent_failed")
	}

	// 5. Try FAQ first.
	reply, faqHit := s.tryFAQ(ctx, intent, content)
	source2 := "faq"
	if faqHit {
		lg.Info().Str("intent", string(intent)).Msg("faq_hit")
	}

	// 6. If FAQ miss, ask LLM.
	if !faqHit {
		source2 = s.cfgLLMProvider()
		lg.Debug().Str("provider", source2).Msg("calling_llm")
		reply, err = s.callLLM(ctx, content, history)
		if err != nil {
			lg.Error().Err(err).Msg("llm_call_failed")
			return nil, err
		}
		lg.Debug().Int("reply_len", len(reply)).Msg("llm_reply_received")
	}

	// 7. Handover decision (Jev noul, with heuristic fallback).
	handed, handoverReason := s.shouldHandover(ctx, conv.ID, intent, conf, content, history)
	if handed {
		lg.Info().
			Str("conv_id", conv.ID).
			Str("reason", handoverReason).
			Float64("intent_confidence", conf).
			Msg("handover_triggered")
	}

	// 8. Persist assistant reply.
	assistant := &model.Message{
		ConversationID: conv.ID,
		Role:           model.RoleAssistant,
		Content:        reply,
		Model:          source2,
	}
	if err := s.Msgs.Insert(ctx, assistant); err != nil {
		lg.Error().Err(err).Msg("persist_assistant_failed")
		return nil, apperr.Internal("persist reply").WithCause(err)
	}
	if handed {
		if err := s.Convs.SetHandedOver(ctx, conv.ID); err != nil {
			lg.Warn().Err(err).Str("conv_id", conv.ID).Msg("mark_handed_over_failed")
		}
		if err := s.Signals.Insert(ctx, &model.HandoverSignal{
			ConversationID: conv.ID,
			Source:         handoverReason,
			Detail:         fmt.Sprintf("intent=%s conf=%.2f", intent, conf),
		}); err != nil {
			lg.Warn().Err(err).Msg("insert_signal_failed")
		}
		// Prepend a clear marker to the assistant text so the client knows.
		if !strings.Contains(reply, "人工") {
			reply = reply + "\n\n—— 系统提示：已为您转接人工客服，客服会在工作时间尽快回复。"
		}
		assistant.Content = reply
		if _, err := s.Msgs.DBConn().ExecContext(ctx,
			`UPDATE messages SET content=? WHERE id=?`, reply, assistant.ID); err != nil {
			lg.Warn().Err(err).Msg("update_assistant_after_handover_failed")
		}
	}

	if err := s.Convs.Touch(ctx, conv.ID); err != nil {
		lg.Warn().Err(err).Msg("touch_conv_after_reply_failed")
	}

	lg.Info().
		Str("conv_id", conv.ID).
		Str("intent", string(intent)).
		Str("source", source2).
		Bool("handed_over", handed).
		Msg("chat_request_done")

	return &ChatResponse{
		ConversationID: conv.ID,
		Message: ChatMessage{
			ID:        assistant.ID,
			Role:      assistant.Role,
			Content:   assistant.Content,
			Model:     assistant.Model,
			CreatedAt: assistant.CreatedAt,
		},
		Intent:           intent,
		IntentConfidence: conf,
		HandedOver:       handed,
		Source:           source2,
		UserMessage: ChatMessage{
			ID:        userMsg.ID,
			Role:      userMsg.Role,
			Content:   userMsg.Content,
			Model:     userMsg.Model,
			CreatedAt: userMsg.CreatedAt,
		},
	}, nil
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

func (s *Chat) resolveConversation(ctx context.Context, req ChatRequest) (*model.Conversation, error) {
	if req.ConversationID != "" {
		c, err := s.Convs.Get(ctx, req.ConversationID)
		if err != nil {
			return nil, apperr.NotFound("conversation not found").WithCause(err)
		}
		if c.UserID != req.UserID {
			return nil, apperr.Forbidden("conversation does not belong to this user")
		}
		return c, nil
	}
	title := req.Content
	if len([]rune(title)) > 40 {
		title = string([]rune(title)[:40])
	}
	c, err := s.Convs.Create(ctx, req.UserID, title)
	if err != nil {
		return nil, apperr.Internal("create conversation").WithCause(err)
	}
	return c, nil
}

type intentSource string

const (
	intentSourceJev      intentSource = "jev"
	intentSourceLLM      intentSource = "llm"
	intentSourceFallback intentSource = "fallback"
)

func (s *Chat) classifyIntent(ctx context.Context, content string, history []model.Message) (model.Intent, float64, intentSource, error) {
	lg := log.With(ctx, s.Logger)
	recent := make([]string, 0, len(history))
	for _, m := range history {
		if m.Role == model.RoleUser || m.Role == model.RoleAssistant {
			recent = append(recent, string(m.Role)+": "+m.Content)
		}
	}
	if s.JEV.Enabled() {
		lg.Debug().Msg("intent_via_jev")
		intent, conf, err := s.JEV.ClassifyIntent(ctx, content, recent)
		if err == nil && intent != model.IntentUnknown {
			return intent, conf, intentSourceJev, nil
		}
		if err != nil {
			lg.Warn().Err(err).Msg("jev_intent_failed_falling_back_llm")
		}
	}
	intent, conf, err := s.classifyIntentWithLLM(ctx, content, recent)
	if err != nil {
		return model.IntentUnknown, 0, intentSourceFallback, err
	}
	return intent, conf, intentSourceLLM, nil
}

func (s *Chat) classifyIntentWithLLM(ctx context.Context, content string, recent []string) (model.Intent, float64, error) {
	prompt := `你是意图分类器。阅读用户的最新消息，按以下四个标签中最匹配的一个输出 JSON：
{"intent":"refund|order|tech|other","confidence":0~1}
退款相关=refund；订单/物流=order；技术/账户/支付故障=tech；其它闲聊=other。
仅输出合法 JSON，不要解释。`
	msgs := []llm.Message{{Role: llm.RoleUser, Content: content}}
	raw, err := s.LLM.Chat(ctx, prompt, msgs)
	if err != nil {
		return model.IntentUnknown, 0, err
	}
	raw = strings.TrimSpace(raw)
	intent, conf, err := parseIntentJSON(raw)
	if err != nil {
		return model.IntentUnknown, 0, err
	}
	return intent, conf, nil
}

func (s *Chat) tryFAQ(ctx context.Context, intent model.Intent, content string) (string, bool) {
	faqs, err := s.FAQs.ListEnabled(ctx, "")
	if err != nil || len(faqs) == 0 {
		return "", false
	}

	best, bestScore := -1, 0
	lc := strings.ToLower(content)
	for i, f := range faqs {
		score := 0
		for _, kw := range f.Keywords {
			if kw == "" {
				continue
			}
			if strings.Contains(lc, strings.ToLower(kw)) {
				score += 2
			}
		}
		if strings.Contains(lc, strings.ToLower(f.Question)) {
			score += 5
		}
		// Intent boost when categories match.
		if intent != model.IntentUnknown && f.Category == intent {
			score += 1
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if best < 0 || bestScore < 2 {
		return "", false
	}
	return faqs[best].Answer, true
}

func (s *Chat) callLLM(ctx context.Context, content string, history []model.Message) (string, error) {
	system := "你是我们的智能客服助手，名字叫“小助理”。回答应当简洁、有礼貌、用中文。如果不确定就说“不确定”，不要编造。"
	msgs := make([]llm.Message, 0, len(history)+1)
	for _, m := range history {
		if m.Role == model.RoleUser || m.Role == model.RoleAssistant {
			msgs = append(msgs, llm.Message{Role: llm.Role(string(m.Role)), Content: m.Content})
		}
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: content})
	return s.LLM.Chat(ctx, system, msgs)
}

func (s *Chat) shouldHandover(ctx context.Context, convID string, intent model.Intent, conf float64, content string, history []model.Message) (bool, string) {
	lc := strings.ToLower(content)
	if strings.Contains(lc, "人工") || strings.Contains(lc, "转人工") || strings.Contains(lc, "真人") || strings.Contains(lc, "human") {
		return true, "user_request"
	}

	if s.JEV.Enabled() {
		recent := make([]string, 0, len(history)+1)
		for _, m := range history {
			recent = append(recent, string(m.Role)+": "+m.Content)
		}
		recent = append(recent, "user: "+content)
		need, _, err := s.JEV.NeedsHandover(ctx, content, recent)
		if err == nil && need {
			return true, "jev"
		}
	}

	if s.HandoverCfg.ConfidenceThreshold > 0 && conf > 0 && conf < s.HandoverCfg.ConfidenceThreshold && intent == model.IntentUnknown {
		return true, "fallback_low_conf"
	}

	if s.HandoverCfg.SignalCountLimit > 0 {
		n, err := s.Signals.CountByConv(ctx, convID)
		if err == nil && n+1 >= s.HandoverCfg.SignalCountLimit {
			return true, "fallback_signal_count"
		}
	}

	return false, ""
}

func (s *Chat) cfgLLMProvider() string {
	id := s.LLM.Identity()
	if id.Provider != "" {
		return id.Provider + ":" + id.Name
	}
	return "llm"
}

func parseIntentJSON(raw string) (model.Intent, float64, error) {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "{"); i >= 0 {
		if j := strings.LastIndex(raw, "}"); j > i {
			raw = raw[i : j+1]
		}
	}
	var v struct {
		Intent     string  `json:"intent"`
		Confidence float64 `json:"confidence"`
	}
	if err := jsonUnmarshal(raw, &v); err != nil {
		return model.IntentUnknown, 0, err
	}
	switch v.Intent {
	case "refund":
		return model.IntentRefund, v.Confidence, nil
	case "order":
		return model.IntentOrder, v.Confidence, nil
	case "tech":
		return model.IntentTech, v.Confidence, nil
	case "other":
		return model.IntentOther, v.Confidence, nil
	}
	return model.IntentUnknown, 0, errors.New("invalid intent label")
}

// nullStr is duplicated here so this package doesn't pull in repo.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}