// AgentChat is the streaming service for /api/chat. It owns the agent
// loop's plumbing:
//
//   1. Resolve the conversation (load existing or create new)
//   2. Persist the user message
//   3. Load recent history
//   4. Open an httpSink and run the agent
//   5. Persist the assistant message
//   6. Mark conversation handed-over if the agent decided so
//
// v2.1.1: when JEV is wired, the service also fires three Jev decision
// points (per docs/v2-jev-rollout.md §2.2):
//
//   - TriggerPreIngest   on the incoming user message   → blocks + handover on "violate"
//   - TriggerMessageEntry on the incoming user message  → informational (intent logging)
//   - TriggerPreReply    on the assistant final text    → informational + audit row
//
// The block-on-violate behaviour is opt-in via BlockOnSensitive (default
// true when JEV is set). Disable it for unit tests / dry-runs.
//
// The service is HTTP-agnostic; it exposes AgentChat.ServeSink so the
// handler can wire it into the response writer.

package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/agent"
	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/llm"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/model"
	"intelligent_customer/backend/internal/repo"
	"intelligent_customer/backend/internal/skill"
)

// AgentChat is the streaming agent service.
type AgentChat struct {
	Convs         *repo.Conversations
	Msgs          *repo.Messages
	Feedbacks     *repo.Feedback
	Registry      *skill.Registry
	Invocations   *skill.Invocations
	Tickets       *skill.Tickets
	LLM           llm.ToolChatCompleter
	HandoverCfg   HandoverConfig
	Cfg           agent.Config
	Logger        zerolog.Logger
	// JEV (v2.1.1): when non-nil, the service fires 3 Jev decision
	// points around each chat. See file docstring.
	JEV *jev.Orchestrator
	// BlockOnSensitive controls whether a "violate" pre_ingest decision
	// hands the conversation over immediately. Defaults to true when
	// JEV is set (callers can override for tests / dry-runs).
	BlockOnSensitive *bool
}

// ServeRequest is the public input. Field names match the JSON the
// handler decodes from the wire.
type ServeRequest struct {
	ConversationID string `json:"conversationId"`
	UserID         string `json:"userId"`
	Content        string `json:"content"`
}

// PersistedAssistant is what the service writes back to the client AFTER
// the NDJSON stream. It carries the IDs so the frontend can scroll to
// the persisted assistant bubble and (optionally) re-fetch.
type PersistedAssistant struct {
	ConversationID   string         `json:"conversationId"`
	UserMessageID    string         `json:"userMessageId"`
	AssistantMessage ChatMessage    `json:"assistantMessage"`
	HandedOver       bool           `json:"handedOver"`
	HandoverReason   string         `json:"handoverReason"`
	Source           string         `json:"source"`
	PendingTicketID  string         `json:"pendingTicketId,omitempty"`
}

// ServeHTTP is the http.Handler entry point. It writes:
//   - one NDJSON event per line (`Content-Type: application/x-ndjson`)
//   - a final {"type":"done","assistant":{...}} line carrying the
//     persisted assistant message + ids
func (s *AgentChat) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), s.Logger).With().Str("endpoint", "agent_chat").Logger()

	// Decode body. Keep limit at 64 KiB to match the old handler.
	var body ServeRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	if err := dec.Decode(&body); err != nil {
		lg.Warn().Err(err).Msg("agent_chat_decode_failed")
		s.writeHTTPError(w, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}

	// Pre-validate BEFORE starting the stream. Errors here can still be
	// served as plain JSON with the correct HTTP status; once the
	// streaming response is committed we can only emit one final
	// {type:error} line.
	if strings.TrimSpace(body.Content) == "" {
		s.writeHTTPError(w, apperr.BadRequest("message content required"))
		return
	}

	sink := &httpSink{w: w}
	res, err := s.Handle(r.Context(), body, sink)
	if err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			lg.Warn().Err(err).Str("code", string(ae.Code)).Int("http", ae.HTTPStatus).Msg("agent_chat_warn")
		} else {
			lg.Error().Err(err).Msg("agent_chat_error")
		}
		// If we already started streaming, we can't switch to JSON now —
		// the body is already committed. Best we can do is emit a final
		// error event and close the stream.
		_ = s.writeFinalError(w, err)
		return
	}

	// Send the final {type:done} event after persistence is complete.
	_ = writeEvent(w, agent.ServeEvent{
		Type:    "done",
		TraceID: log.RequestIDFrom(r.Context()),
	})
	lg.Info().
		Str("conv_id", res.ConversationID).
		Bool("handed_over", res.HandedOver).
		Str("source", res.Source).
		Str("handover_reason", res.HandoverReason).
		Msg("agent_chat_done")
}

// Handle is the non-streaming variant. Useful for tests; production goes
// through ServeHTTP. It returns the persisted result plus any error.
// The sink may be nil — in that case only the persisted assistant is
// returned (no streaming).
func (s *AgentChat) Handle(ctx context.Context, req ServeRequest, sink agent.Sink) (*PersistedAssistant, error) {
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
		Msg("agent_chat_request_received")

	// v2.1.1 Jev trigger point: message.entry (intent routing).
	// Runs BEFORE persistence so a misconfigured tenant / disabled Jev
	// never blocks a real chat. Failures are logged + ignored — the
	// existing agent loop remains the source of truth for routing.
	s.fireJevEntry(ctx, content)

	// Resolve / create conversation.
	conv, err := s.resolveConversation(ctx, req)
	if err != nil {
		return nil, err
	}

	// v2.1.1 Jev trigger point: message.pre_ingest (sensitive check).
	// Runs AFTER conv resolution but BEFORE persisting the user message
	// so a "violate" verdict can short-circuit without polluting the
	// transcript. Default behaviour when JEV is wired: violate → handover.
	if blocked, reason := s.fireJevPreIngest(ctx, content); blocked {
		// Block-and-handover path. Persist the user message first so
		// the audit trail captures the attempt, then write a fixed
		// assistant reply explaining the block + mark the conversation
		// as handed over.
		userMsg := &model.Message{
			ConversationID: conv.ID,
			Role:           model.RoleUser,
			Content:        content,
		}
		if err := s.Msgs.Insert(ctx, userMsg); err != nil {
			return nil, apperr.Internal("persist user message").WithCause(err)
		}
		return s.persistBlockedByJev(ctx, conv.ID, userMsg.ID, reason)
	}

	// Persist user message.
	userMsg := &model.Message{
		ConversationID: conv.ID,
		Role:           model.RoleUser,
		Content:        content,
	}
	if err := s.Msgs.Insert(ctx, userMsg); err != nil {
		return nil, apperr.Internal("persist user message").WithCause(err)
	}
	_ = s.Convs.Touch(ctx, conv.ID)

	// Load history.
	history, err := s.Msgs.LastN(ctx, conv.ID, 12)
	if err != nil {
		return nil, apperr.Internal("load history").WithCause(err)
	}

	// First, check hard handover triggers (user said "人工") — this is a
	// fast-path so we don't burn LLM tokens on a request we know we'll
	// refuse anyway.
	if wantHandover(content) {
		_ = s.emitHandover(sink, "user_request", "", nil)
		return s.persistHandover(ctx, conv.ID, userMsg.ID, "user_request", "用户明确要求转人工。客服会在工作时间尽快与您联系。")
	}

	// Configure the httpSink fallback when the caller passed nil.
	if sink == nil {
		sink = &agent.MemSink{}
	}

	// Run the agent loop. trace_id is propagated through req ctx.
	traceID := log.RequestIDFrom(ctx)
	runRes, err := agent.NewRunner().Run(ctx, agent.RunRequest{
		TraceID:        traceID,
		UserID:         req.UserID,
		ConversationID: conv.ID,
		UserContent:    content,
		History:        history,
		Registry:       s.Registry,
		LLM:            s.LLM,
		Invocations:    s.Invocations,
		Config:         s.Cfg,
		Logger:         s.Logger,
	}, sink)

	if err != nil {
		return nil, err
	}

	// Persist assistant message.
	assistant := &model.Message{
		ConversationID: conv.ID,
		Role:           model.RoleAssistant,
		Content:        runRes.FinalContent,
		Model:          s.LMProviderLabel(),
	}
	if err := s.Msgs.Insert(ctx, assistant); err != nil {
		return nil, apperr.Internal("persist reply").WithCause(err)
	}

	// v2.1.1 Jev trigger point: message.pre_reply (sensitive check on
	// the assistant output). Runs AFTER persistence so the audit trail
	// always captures the actual reply; the verdict only informs
	// future gating and never re-writes the stored transcript.
	s.fireJevPreReply(ctx, runRes.FinalContent)

	if runRes.HandedOver {
		_ = s.Convs.SetHandedOver(ctx, conv.ID)
		_ = s.Signals().Insert(ctx, &model.HandoverSignal{
			ConversationID: conv.ID,
			Source:         runRes.HandoverReason,
			Detail:         fmt.Sprintf("agent_steps=%d", len(runRes.Steps)),
		})
		// Prepend a clear marker to the assistant text.
		if !strings.Contains(runRes.FinalContent, "人工") {
			assistant.Content = runRes.FinalContent + "\n\n—— 系统提示：已为您转接人工客服，客服会在工作时间尽快回复。"
		} else {
			assistant.Content = runRes.FinalContent
		}
		if _, err := s.Msgs.DBConn().ExecContext(ctx,
			`UPDATE messages SET content=? WHERE id=?`, assistant.Content, assistant.ID); err != nil {
			lg.Warn().Err(err).Msg("agent_update_after_handover_failed")
		}
	}

	_ = s.Convs.Touch(ctx, conv.ID)

	// If a pending ticket was created, surface its id so the frontend
	// can prompt the user to confirm.
	var ticketID string
	for _, st := range runRes.Steps {
		for _, tu := range st.ToolCalls {
			if tu.Result.PendingTicket != "" {
				ticketID = tu.Result.PendingTicket
			}
		}
	}

	if runRes.HandedOver {
		_ = s.emitHandover(sink, runRes.HandoverReason, "", nil)
	}

	return &PersistedAssistant{
		ConversationID: conv.ID,
		UserMessageID:  userMsg.ID,
		AssistantMessage: ChatMessage{
			ID:        assistant.ID,
			Role:      assistant.Role,
			Content:   assistant.Content,
			Model:     assistant.Model,
			CreatedAt: assistant.CreatedAt,
		},
		HandedOver:      runRes.HandedOver,
		HandoverReason:  runRes.HandoverReason,
		Source:          runRes.Source,
		PendingTicketID: ticketID,
	}, nil
}

// ----------------------------------------------------------------------------
// helpers (kept private — service-layer only)
// ----------------------------------------------------------------------------

func (s *AgentChat) resolveConversation(ctx context.Context, req ServeRequest) (*model.Conversation, error) {
	lg := log.With(ctx, s.Logger)
	if req.ConversationID != "" {
		c, err := s.Convs.Get(ctx, req.ConversationID)
		if err == nil {
			// Found the existing conversation — enforce ownership so a
			// caller can't piggy-back on another user's transcript.
			if c.UserID != req.UserID {
				return nil, apperr.Forbidden("conversation does not belong to this user")
			}
			return c, nil
		}
		// Stale id: the front-end's localStorage may hold an id whose row
		// was deleted (DB reset, cross-device move, cache cleared). Fall
		// through to the create path instead of bubbling up a hard
		// not_found, which surfaces in the chat UI as a red error and
		// forces the user to manually click "new session". This is safe:
		// the new conversation is bound to req.UserID (not the stale id)
		// so there is no cross-user data leakage.
		lg.Warn().
			Str("conv_id", req.ConversationID).
			Str("user_id", req.UserID).
			Msg("resolveConversation: stale conv id, creating new one")
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

// Signals is a tiny indirection so the chat service doesn't have to take
// a repo dependency directly. (It already has one for Convs/Msgs — keep
// the symmetry by lazily constructing the wrapper when the agent
// produces a handover signal.)
func (s *AgentChat) Signals() *repo.HandoverSignals {
	return repo.NewHandoverSignals(s.Msgs.DBConn())
}

// persistHandover records the assistant message with a hard-coded
// handover reply and marks it as handed-over. Used for the user_request
// fast-path.
func (s *AgentChat) persistHandover(ctx context.Context, convID, userMsgID, reason, reply string) (*PersistedAssistant, error) {
	if err := s.Convs.SetHandedOver(ctx, convID); err != nil {
		return nil, apperr.Internal("mark handed over").WithCause(err)
	}
	_ = s.Signals().Insert(ctx, &model.HandoverSignal{
		ConversationID: convID,
		Source:         reason,
		Detail:         "user_request_fast_path",
	})
	asst := &model.Message{
		ConversationID: convID,
		Role:           model.RoleAssistant,
		Content:        reply,
		Model:          "system",
	}
	if err := s.Msgs.Insert(ctx, asst); err != nil {
		return nil, apperr.Internal("persist handover reply").WithCause(err)
	}
	_ = s.Convs.Touch(ctx, convID)
	return &PersistedAssistant{
		ConversationID: convID,
		UserMessageID:  userMsgID,
		AssistantMessage: ChatMessage{
			ID:        asst.ID,
			Role:      asst.Role,
			Content:   asst.Content,
			Model:     asst.Model,
			CreatedAt: asst.CreatedAt,
		},
		HandedOver:     true,
		HandoverReason: reason,
		Source:         "handover",
	}, nil
}

func (s *AgentChat) LMProviderLabel() string {
	return s.LLM.Identity().Provider + ":" + s.LLM.Identity().Name
}

// fireJevEntry is the v2.1.1 Jev trigger point message.entry. Fires
// the orchestrator with the user content + history summary so the
// observability page can count intent classifications even though we
// don't act on the choice yet. Returns nothing — failures are logged.
func (s *AgentChat) fireJevEntry(ctx context.Context, content string) {
	if s.JEV == nil {
		return
	}
	d, err := s.JEV.DecideFromContext(ctx, jev.TriggerMessageEntry, map[string]any{
		"message": content,
	})
	if err != nil {
		// Orchestrator already fell back internally; any error here
		// means fallback also failed. Log + continue.
		log.With(ctx, s.Logger).Warn().Err(err).Msg("jev_entry_decide_failed")
		return
	}
	if d != nil {
		log.With(ctx, s.Logger).Debug().
			Str("intent", d.Choice).
			Bool("fallback", d.Fallback).
			Str("decision_id", d.DecisionID).
			Msg("jev_entry_decided")
	}
}

// fireJevPreIngest is the v2.1.1 Jev trigger point message.pre_ingest.
// Returns (true, reason) when the orchestrator verdict is "violate" AND
// the service is configured to block (BlockOnSensitive defaults to
// true). Any other verdict, missing orchestrator, or non-blocking
// config returns (false, "").
func (s *AgentChat) fireJevPreIngest(ctx context.Context, content string) (bool, string) {
	if s.JEV == nil {
		return false, ""
	}
	if s.BlockOnSensitive != nil && !*s.BlockOnSensitive {
		// Caller disabled blocking — still record the decision via
		// DecideFromContext for observability.
		_, _ = s.JEV.DecideFromContext(ctx, jev.TriggerPreIngest, map[string]any{
			"content": content,
		})
		return false, ""
	}
	d, err := s.JEV.DecideFromContext(ctx, jev.TriggerPreIngest, map[string]any{
		"content": content,
	})
	if err != nil || d == nil {
		return false, ""
	}
	if d.Choice == "violate" {
		return true, fmt.Sprintf("jev_sensitive_violate:%s", d.DecisionID)
	}
	return false, ""
}

// fireJevPreReply is the v2.1.1 Jev trigger point reply.pre_ingest.
// Informational only — runs the decision for audit but never mutates
// the persisted reply. Logs at warn level on "violate" so an operator
// can spot a misbehaving assistant without re-running the conversation.
func (s *AgentChat) fireJevPreReply(ctx context.Context, reply string) {
	if s.JEV == nil {
		return
	}
	d, err := s.JEV.DecideFromContext(ctx, jev.TriggerPreReply, map[string]any{
		"content": reply,
	})
	if err != nil || d == nil {
		return
	}
	lg := log.With(ctx, s.Logger)
	if d.Choice == "violate" {
		lg.Warn().
			Str("decision_id", d.DecisionID).
			Bool("fallback", d.Fallback).
			Int("len", len(reply)).
			Msg("jev_pre_reply_violate")
		return
	}
	lg.Debug().
		Str("decision_id", d.DecisionID).
		Str("verdict", d.Choice).
		Bool("fallback", d.Fallback).
		Msg("jev_pre_reply_decided")
}

// persistBlockedByJev writes a fixed assistant reply when the Jev
// pre_ingest verdict blocks the message, marks the conversation handed
// over, and returns the usual PersistedAssistant. Mirrors the user
// fast-path in persistHandover but exposes the block reason so the
// frontend can show a tailored "本条消息含敏感内容，已转人工审核" hint.
func (s *AgentChat) persistBlockedByJev(ctx context.Context, convID, userMsgID, reason string) (*PersistedAssistant, error) {
	if err := s.Convs.SetHandedOver(ctx, convID); err != nil {
		return nil, apperr.Internal("mark handed over").WithCause(err)
	}
	_ = s.Signals().Insert(ctx, &model.HandoverSignal{
		ConversationID: convID,
		Source:         "jev_violate",
		Detail:         reason,
	})
	const reply = "您发送的消息触发了内容安全策略，已自动转接人工客服复核。客服会在工作时间尽快与您联系。"
	asst := &model.Message{
		ConversationID: convID,
		Role:           model.RoleAssistant,
		Content:        reply,
		Model:          "system+jev",
	}
	if err := s.Msgs.Insert(ctx, asst); err != nil {
		return nil, apperr.Internal("persist block reply").WithCause(err)
	}
	_ = s.Convs.Touch(ctx, convID)
	return &PersistedAssistant{
		ConversationID:   convID,
		UserMessageID:    userMsgID,
		AssistantMessage: ChatMessage{ID: asst.ID, Role: asst.Role, Content: asst.Content, Model: asst.Model, CreatedAt: asst.CreatedAt},
		HandedOver:       true,
		HandoverReason:   "jev_violate",
		Source:           "handover",
	}, nil
}

func wantHandover(content string) bool {
	lc := strings.ToLower(content)
	return strings.Contains(lc, "人工") || strings.Contains(lc, "转人工") ||
		strings.Contains(lc, "真人") || strings.Contains(lc, "human agent") ||
		strings.Contains(lc, "human agent")
}

func (s *AgentChat) emitHandover(sink agent.Sink, reason, _ string, _ map[string]any) error {
	if sink == nil {
		return nil
	}
	return sink.Write(agent.ServeEvent{
		Type:       "handover",
		Reason:     reason,
		HandedOver: true,
	})
}

func (s *AgentChat) writeHTTPError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if ae, ok := apperr.As(err); ok {
		w.WriteHeader(ae.HTTPStatus)
		_ = json.NewEncoder(w).Encode(ae)
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(apperr.Internal("unexpected error"))
}

// writeFinalError writes an error event onto an already-started NDJSON
// stream so the frontend at least knows what happened.
func (s *AgentChat) writeFinalError(w http.ResponseWriter, err error) error {
	return writeEvent(w, agent.ServeEvent{
		Type:  "error",
		Error: err.Error(),
	})
}

// ----------------------------------------------------------------------------
// httpSink — writes NDJSON to an http.ResponseWriter with periodic flush.
// ----------------------------------------------------------------------------

type httpSink struct {
	w       http.ResponseWriter
	once    bool
	mu      chan struct{}
	written int
}

func (h *httpSink) Write(ev agent.ServeEvent) error {
	// Lazily set headers on first write so 4xx errors can still
	// produce a JSON body before streaming starts.
	if !h.once {
		h.w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		h.w.Header().Set("Cache-Control", "no-cache")
		h.w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
		h.w.WriteHeader(http.StatusOK)
		h.once = true
	}
	return writeEvent(h.w, ev)
}

// writeEvent serialises ev to JSON, appends a newline, and flushes.
func writeEvent(w http.ResponseWriter, ev agent.ServeEvent) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err := w.Write(b); err != nil {
		return err
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

// bufioSink is here in case someone wants buffered writes for tests. Not
// wired in production; production uses httpSink directly.
type bufioSink struct {
	w  *bufio.Writer
}

func (b *bufioSink) Write(ev agent.ServeEvent) error {
	jb, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := b.w.Write(jb); err != nil {
		return err
	}
	if err := b.w.WriteByte('\n'); err != nil {
		return err
	}
	// Flush periodically; the agent doesn't write enough events for
	// buffering to matter.
	if b.w.Buffered() > 4096 {
		_ = b.w.Flush()
	}
	return nil
}

// keep time import in use if bufioSink goes unused.
var _ = time.Second
var _ = (*bufioSink)(nil)