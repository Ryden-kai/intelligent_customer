// Package agent is the Agent loop. It wires together:
//   - a ToolChatCompleter (LLM)
//   - the skill Registry (tools the LLM can call)
//   - the audit repo (skill_invocations + skill_pending_tickets)
//   - a stream Sink (NDJSON events for the frontend)
//
// The loop is bounded by max_steps / max_tokens / max_wallclock. Any of
// those tripping causes a graceful handover to a human rather than a
// silent cap.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/llm"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/model"
	"intelligent_customer/backend/internal/skill"
)

// Config tunes the agent loop. Defaults are set by New(); values can be
// overridden via env-driven Config in main.go.
type Config struct {
	MaxSteps     int           // hard cap on LLM steps in one run (default 5)
	MaxTokens    int           // cumulative token budget (default 4000)
	MaxWallclock time.Duration // wall-clock budget for one run (default 20s)
	SkillTimeout time.Duration // per-skill execution timeout (default 5s)
	SystemPrompt string        // base system prompt; tool list is appended at runtime
}

// DefaultConfig returns safe defaults that match the docstring in
// internal/skill/README.md.
func DefaultConfig() Config {
	return Config{
		MaxSteps:     5,
		MaxTokens:    4000,
		MaxWallclock: 20 * time.Second,
		SkillTimeout: 5 * time.Second,
		SystemPrompt: "你是我们的智能客服助手「小助理」。回答应当简洁、有礼貌、用中文。" +
			"你可以调用工具获取真实信息；调用前必须严格按工具的 arguments schema 传参。" +
			"如不确定，就说「不确定」，不要编造；当用户明确说「人工」/「转人工」/「真人」时直接建议转人工。",
	}
}

// Sink is the streaming interface. The service layer implements it on
// top of http.ResponseWriter with a bufio.Writer + flush; tests use a
// in-memory recorder. Events are documented in agent.ServeEvent.
type Sink interface {
	Write(ev ServeEvent) error
}

// ServeEvent is one NDJSON line. JSON tags are stable — the frontend
// reads them by name.
type ServeEvent struct {
	Type       string         `json:"type"` // step | tool_call | tool_result | pending_human | final | handover | done | error
	TraceID    string         `json:"traceId"`
	Step       int            `json:"step,omitempty"`
	Content    string         `json:"content,omitempty"`
	Tool       string         `json:"tool,omitempty"`
	Args       map[string]any `json:"args,omitempty"`
	Result     any            `json:"result,omitempty"`
	Summary    string         `json:"summary,omitempty"`
	Status     string         `json:"status,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	TicketID   string         `json:"ticketId,omitempty"`
	HandedOver bool           `json:"handedOver,omitempty"`
	Source     string         `json:"source,omitempty"`
	DurationMs int64          `json:"durationMs,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// RunRequest is what the service layer hands to the agent.
type RunRequest struct {
	TraceID        string
	UserID         string
	ConversationID string
	UserContent    string
	History        []model.Message // chronological, oldest first; only user/assistant entries are passed to LLM
	Registry       *skill.Registry
	LLM            llm.ToolChatCompleter
	Invocations    *skill.Invocations
	Config         Config
	Logger         zerolog.Logger
}

// RunResult is what the agent returns to the service. The service uses
// it to compose the final persisted assistant message.
type RunResult struct {
	FinalContent   string
	HandedOver     bool
	HandoverReason string
	Source         string // "agent" | "handover"
	Steps          []StepRecord
}

// StepRecord captures what happened in one LLM step for audit + debug.
type StepRecord struct {
	Index     int
	StartedAt time.Time
	Duration  time.Duration
	Content   string
	ToolCalls []ToolUse
	TokenUsed llm.TokenUsage
}

// ToolUse is one executed tool call inside a step.
type ToolUse struct {
	Name      string
	Args      string
	Result    skill.ExecutionResult
	StartedAt time.Time
	Duration  time.Duration
}

// ----------------------------------------------------------------------------
// Runner
// ----------------------------------------------------------------------------

type Runner struct{}

func NewRunner() *Runner { return &Runner{} }

// Run executes the agent loop. ctx must already carry the trace id (via
// middleware.RequestID); the trace id is also embedded in every
// ServeEvent so the frontend can correlate.
//
// Loop shape:
//
//  1. Build the system prompt (base + tool list).
//  2. Translate model.Message history → llm.Message list.
//  3. For each step (up to MaxSteps):
//     a. Call LLM.ToolChat.
//     b. If no tool_calls → reply directly. Append assistant content,
//        send final event, exit loop.
//     c. Else → for each tool_call: execute (with per-skill timeout),
//        append tool result message, send tool_call + tool_result events.
//        Special-case pending_human: emit pending_human event, exit loop
//        with FinalContent = human-facing summary.
//  4. On loop exit, check trip reasons. If max_steps hit but LLM didn't
//     produce a final answer → handover with reason "max_steps".
func (r *Runner) Run(ctx context.Context, req RunRequest, sink Sink) (RunResult, error) {
	if req.TraceID == "" {
		req.TraceID = uuid.NewString()
	}
	lg := log.With(ctx, req.Logger).With().
		Str("trace_id", req.TraceID).
		Str("conv_id", req.ConversationID).
		Str("user_id", req.UserID).
		Logger()

	cfg := req.Config
	if cfg.MaxSteps <= 0 {
		cfg = DefaultConfig()
	}

	// Outer wallclock cap — every step shares this budget.
	runCtx, cancel := context.WithTimeout(ctx, cfg.MaxWallclock)
	defer cancel()

	results := RunResult{Source: "agent"}
	results.Steps = make([]StepRecord, 0, cfg.MaxSteps)

	// Build llm-side conversation: history + new user message.
	llmMsgs := make([]llm.Message, 0, len(req.History)+2)
	for _, m := range req.History {
		if m.Role != model.RoleUser && m.Role != model.RoleAssistant {
			continue
		}
		llmMsgs = append(llmMsgs, llm.Message{Role: llm.Role(string(m.Role)), Content: m.Content})
	}
	if req.UserContent != "" {
		llmMsgs = append(llmMsgs, llm.Message{Role: llm.RoleUser, Content: req.UserContent})
	}

	tools := snapshotTools(req.Registry)
	if len(tools) == 0 {
		lg.Warn().Msg("agent_no_tools_registered")
	}
	systemPrompt := composeSystemPrompt(cfg.SystemPrompt, tools)

	stepUsed := 0
	for step := 1; step <= cfg.MaxSteps; step++ {
		select {
		case <-runCtx.Done():
			return handoverResult(results, "max_wallclock"), nil
		default:
		}

		stepRec := StepRecord{Index: step, StartedAt: time.Now()}

		_ = sink.Write(ServeEvent{
			Type:    "step",
			TraceID: req.TraceID,
			Step:    step,
			Content: fmt.Sprintf("思考中（第 %d 步，共 %d 个 skill 可用）…", step, len(tools)),
		})

		// LLM call. We don't wrap a per-step ctx — the run budget covers it.
		t0 := time.Now()
		res, err := req.LLM.ToolChat(runCtx, systemPrompt, llmMsgs, tools)
		stepRec.Duration = time.Since(t0)
		if err != nil {
			// Hard LLM failure → handover. Don't try to recover silently.
			lg.Error().Err(err).Int("step", step).Msg("agent_llm_failed")
			_ = sink.Write(ServeEvent{
				Type:  "error",
				Step:  step,
				Error: err.Error(),
			})
			return handoverResult(results, "llm_error"), nil
		}
		stepRec.TokenUsed = res.Usage
		stepUsed += res.Usage.TotalTokens
		stepRec.Content = res.Content

		// If LLM decided not to call any tool → final reply.
		if len(res.ToolCalls) == 0 {
			results.Steps = append(results.Steps, stepRec)
			final := strings.TrimSpace(res.Content)
			if final == "" {
				final = "（本次未生成回答，已转人工）"
			}
			results.FinalContent = final
			results.Source = "agent"
			_ = sink.Write(ServeEvent{
				Type:       "final",
				TraceID:    req.TraceID,
				Step:       step,
				Content:    final,
				Source:     "agent",
				DurationMs: stepRec.Duration.Milliseconds(),
			})
			return results, nil
		}

		// Append the assistant message with tool_calls to the history so
		// the next iteration has full context.
		llmMsgs = append(llmMsgs, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   res.Content,
			ToolCalls: res.ToolCalls,
		})

		// Execute each tool call. Tool results are appended to llmMsgs
		// for the next step. If any tool returns pending_human we exit
		// the loop immediately.
		pendingHuman := false
		for _, tc := range res.ToolCalls {
			tu, err := r.executeTool(ctx, req, tc, step, sink)
			if err != nil {
				stepRec.ToolCalls = append(stepRec.ToolCalls, ToolUse{
					Name:      tc.Name,
					Args:      tc.Arguments,
					StartedAt: time.Now(),
					Duration:  0,
					Result: skill.ExecutionResult{
						Status:  "error",
						Data:    map[string]any{"error": err.Error()},
						Summary: err.Error(),
					},
				})
				llmMsgs = append(llmMsgs, llm.Message{
					Role:       llm.RoleTool,
					ToolCallID: tc.ID,
					ToolName:   tc.Name,
					Content:    fmt.Sprintf(`{"error":%q}`, err.Error()),
				})
				_ = sink.Write(ServeEvent{
					Type:   "tool_result",
					Step:   step,
					Tool:   tc.Name,
					Args:   parseArgs(tc.Arguments),
					Result: map[string]any{"error": err.Error()},
					Status: "error",
				})
				continue
			}
			stepRec.ToolCalls = append(stepRec.ToolCalls, tu)
			resultJSON := marshalData(tu.Result)
			llmMsgs = append(llmMsgs, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Content:    resultJSON,
			})
			_ = sink.Write(ServeEvent{
				Type:       "tool_result",
				TraceID:    req.TraceID,
				Step:       step,
				Tool:       tc.Name,
				Args:       parseArgs(tc.Arguments),
				Result:     tu.Result.Data,
				Summary:    tu.Result.Summary,
				Status:     tu.Result.Status,
				TicketID:   tu.Result.PendingTicket,
				DurationMs: tu.Duration.Milliseconds(),
			})
			if tu.Result.Status == skill.StatusPendingHuman {
				pendingHuman = true
				results.FinalContent = tu.Result.Summary
				results.Source = "agent"
				_ = sink.Write(ServeEvent{
					Type:     "pending_human",
					TraceID:  req.TraceID,
					Step:     step,
					Tool:     tc.Name,
					TicketID: tu.Result.PendingTicket,
					Summary:  tu.Result.Summary,
				})
				break
			}
		}

		results.Steps = append(results.Steps, stepRec)

		if pendingHuman {
			return results, nil
		}

		if stepUsed >= cfg.MaxTokens {
			lg.Warn().Int("tokens", stepUsed).Msg("agent_token_budget_exceeded")
			return handoverResult(results, "max_tokens"), nil
		}
	}

	// Loop fell off the end without a final reply.
	lg.Warn().Int("max_steps", cfg.MaxSteps).Msg("agent_max_steps_hit")
	return handoverResult(results, "max_steps"), nil
}

func (r *Runner) executeTool(ctx context.Context, req RunRequest, tc llm.ToolCall, step int, sink Sink) (ToolUse, error) {
	lg := log.With(ctx, req.Logger)
	def, err := req.Registry.Get(tc.Name)
	if err != nil {
		return ToolUse{}, fmt.Errorf("skill %q: %w", tc.Name, err)
	}

	// Inject request-level context that the LLM shouldn't have to
	// remember (current user id, conversation id). The skill gets
	// the augmented JSON; we log both for audit clarity.
	args := injectRequestContext(tc.Arguments, req.UserID, req.ConversationID)
	if args != tc.Arguments {
		lg.Debug().
			Str("skill", tc.Name).
			Str("user_id", req.UserID).
			Msg("agent_skill_args_injected")
	}

	// Emit tool_call event up-front so the frontend can show "正在调用…".
	_ = sink.Write(ServeEvent{
		Type:    "tool_call",
		TraceID: req.TraceID,
		Step:    step,
		Tool:    tc.Name,
		Args:    parseArgs(args),
	})

	t0 := time.Now()
	result, err := skill.ExecuteWithTimeout(ctx, def, args, req.Config.SkillTimeout)
	tu := ToolUse{
		Name:      tc.Name,
		Args:      args,
		Result:    result,
		StartedAt: t0,
		Duration:  time.Since(t0),
	}
	if err != nil {
		_ = recordInvocation(req, tc, step, result, err.Error(), "", tu.Duration)
		lg.Error().Err(err).Str("skill", tc.Name).Msg("agent_skill_failed")
		return tu, err
	}

	// Update the pending ticket's conversation id now that we know it
	// (the skill doesn't see conv id; only user id).
	if result.Status == skill.StatusPendingHuman && req.ConversationID != "" {
		if _, err := req.Invocations.DB.ExecContext(ctx,
			`UPDATE skill_pending_tickets SET conversation_id=? WHERE id=? AND conversation_id = ''`,
			req.ConversationID, result.PendingTicket); err != nil {
			lg.Warn().Err(err).Msg("agent_update_ticket_conv_failed")
		}
	}

	_ = recordInvocation(req, tc, step, result, "", result.PendingTicket, tu.Duration)
	return tu, nil
}

func recordInvocation(req RunRequest, tc llm.ToolCall, step int, result skill.ExecutionResult, errMsg, ticket string, dur time.Duration) error {
	if req.Invocations == nil {
		return nil
	}
	argsJSON := tc.Arguments
	if argsJSON == "" {
		argsJSON = "{}"
	}
	resJSON := marshalData(result)
	status := result.Status
	if errMsg != "" {
		status = "error"
	}
	inv := &model.SkillInvocation{
		ConversationID: req.ConversationID,
		SkillName:      tc.Name,
		ArgsJSON:       argsJSON,
		ResultJSON:     resJSON,
		Status:         status,
		PendingTicket:  ticket,
		TraceID:        req.TraceID,
		StepIndex:      step,
		DurationMS:     dur.Milliseconds(),
	}
	return req.Invocations.Record(context.Background(), inv)
}

func handoverResult(partial RunResult, reason string) RunResult {
	partial.HandedOver = true
	partial.HandoverReason = reason
	partial.Source = "handover"
	return partial
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// injectRequestContext merges request-level fields (user_id,
// conversation_id) into the tool-call args if the LLM didn't supply
// them. Returns the original JSON unchanged if it's not a JSON object,
// so we don't crash on exotic inputs. The injected fields are
// intentionally NOT added to the LLM-facing JSON schema — the LLM
// shouldn't be tempted to invent them.
func injectRequestContext(argsJSON, userID, convID string) string {
	if userID == "" && convID == "" {
		return argsJSON
	}
	if argsJSON == "" {
		argsJSON = "{}"
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return argsJSON
	}
	if m == nil {
		m = map[string]any{}
	}
	if userID != "" {
		if _, ok := m["user_id"]; !ok {
			m["user_id"] = userID
		}
	}
	if convID != "" {
		if _, ok := m["conversation_id"]; !ok {
			m["conversation_id"] = convID
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return argsJSON
	}
	return string(b)
}

func snapshotTools(reg *skill.Registry) []llm.ToolDef {
	if reg == nil {
		return nil
	}
	snap := reg.Snapshot()
	out := make([]llm.ToolDef, 0, len(snap))
	for _, d := range snap {
		params := d.ParametersJSON
		if params == "" {
			params = `{"type":"object","properties":{}}`
		}
		out = append(out, llm.ToolDef{
			Name:        d.Name,
			Description: d.Description,
			Parameters:  json.RawMessage(params),
		})
	}
	return out
}

func composeSystemPrompt(base string, tools []llm.ToolDef) string {
	if len(tools) == 0 {
		return base
	}
	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("\n\n可用工具：\n")
	for _, t := range tools {
		fmt.Fprintf(&sb, "- %s: %s\n", t.Name, t.Description)
		fmt.Fprintf(&sb, "  参数: %s\n", string(t.Parameters))
	}
	sb.WriteString("\n如需调用工具，按工具的 JSON Schema 传 arguments。")
	return sb.String()
}

func parseArgs(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return map[string]any{"_raw": raw}
	}
	return m
}

func marshalData(r skill.ExecutionResult) string {
	if r.Data == nil && r.Summary == "" && r.Status == "" {
		return ""
	}
	type wrap struct {
		Status        string         `json:"status"`
		Summary       string         `json:"summary"`
		Data          map[string]any `json:"data,omitempty"`
		PendingTicket string         `json:"pendingTicket,omitempty"`
	}
	b, _ := json.Marshal(wrap{Status: r.Status, Summary: r.Summary, Data: r.Data, PendingTicket: r.PendingTicket})
	return string(b)
}

// ----------------------------------------------------------------------------
// In-memory sink (for tests + non-streaming callers)
// ----------------------------------------------------------------------------

// MemSink is a Sink that buffers events into a slice. Use for tests; in
// production the service layer uses an httpSink that writes NDJSON.
type MemSink struct {
	mu     sync.Mutex
	events []ServeEvent
}

func (m *MemSink) Write(ev ServeEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, ev)
	return nil
}

func (m *MemSink) Events() []ServeEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ServeEvent, len(m.events))
	copy(out, m.events)
	return out
}

// compile-time check
var _ Sink = (*MemSink)(nil)