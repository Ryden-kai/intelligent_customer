package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/agent"
	"intelligent_customer/backend/internal/llm"
	"intelligent_customer/backend/internal/skill"
)

// scriptedLLM is a stub llm.ToolChatCompleter whose behaviour is dictated
// by the test. It records every call so the test can assert call shape
// after the run.
//
// script is a slice of step responses; each step is either:
//   "FINAL:<text>" — return this as plain content, no tool calls
//   "TOOL:<tool>:<args>" — return one tool call with the given name + args
//   "ERR:<text>" — return error from ToolChat (e.g. simulated upstream failure)
// When the script is exhausted, the LLM returns whatever the last entry was
// (so a test with a single "FINAL" entry gets a one-shot final reply).
type scriptedLLM struct {
	script []string
	calls  int
}

func (s *scriptedLLM) Identity() llm.Model { return llm.Model{Provider: "stub", Name: "stub-llm"} }

func (s *scriptedLLM) ToolChat(_ context.Context, _ string, msgs []llm.Message, tools []llm.ToolDef) (llm.ToolChatResult, error) {
	idx := s.calls
	s.calls++
	if idx >= len(s.script) {
		idx = len(s.script) - 1
	}
	line := s.script[idx]
	switch {
	case strings.HasPrefix(line, "FINAL:"):
		return llm.ToolChatResult{Content: strings.TrimPrefix(line, "FINAL:"), Usage: llm.TokenUsage{TotalTokens: 5}}, nil
	case strings.HasPrefix(line, "TOOL:"):
		rest := strings.TrimPrefix(line, "TOOL:")
		parts := strings.SplitN(rest, ":", 2)
		name := parts[0]
		args := "{}"
		if len(parts) > 1 {
			args = parts[1]
		}
		return llm.ToolChatResult{
			Content: "",
			ToolCalls: []llm.ToolCall{{
				ID:        "call-" + itoa(idx),
				Name:      name,
				Arguments: args,
			}},
			Usage: llm.TokenUsage{TotalTokens: 7},
		}, nil
	case strings.HasPrefix(line, "ERR:"):
		return llm.ToolChatResult{}, errStub(strings.TrimPrefix(line, "ERR:"))
	default:
		return llm.ToolChatResult{Content: line}, nil
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var s string
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// TestAgentNoToolsReturnsFinal verifies the loop ends with a final event
// when no tools are available — the LLM just returns a content reply.
func TestAgentNoToolsReturnsFinal(t *testing.T) {
	registry := skill.NewRegistry()
	llm := &scriptedLLM{script: []string{"FINAL:你好，这是一个测试。"}}
	sink := &agent.MemSink{}
	res, err := (&agent.Runner{}).Run(context.Background(), agent.RunRequest{
		TraceID: "trace-1", UserID: "u1", UserContent: "你好",
		Registry: registry, LLM: llm,
		Invocations: nil, Config: agent.DefaultConfig(), Logger: zerolog.Nop(),
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.FinalContent, "测试") {
		t.Fatalf("final content = %q", res.FinalContent)
	}
	// Look for a "final" event in the sink.
	hasFinal := false
	for _, ev := range sink.Events() {
		if ev.Type == "final" {
			hasFinal = true
		}
	}
	if !hasFinal {
		t.Fatalf("no final event; got %+v", sink.Events())
	}
	if llm.calls != 1 {
		t.Fatalf("expected 1 LLM call, got %d", llm.calls)
	}
}

// TestAgentSingleToolCall exercises the loop: tool_call → tool_result → final.
func TestAgentSingleToolCall(t *testing.T) {
	registry := skill.NewRegistry()
	if err := registry.Register(skill.Definition{
		Name:           "echo",
		Description:    "echo back",
		ParametersJSON: `{"type":"object","properties":{"msg":{"type":"string"}}}`,
		ReadOnly:       true,
		Enabled:        true,
		Execute: func(_ context.Context, argsJSON string) (skill.ExecutionResult, error) {
			var a struct {
				Msg string `json:"msg"`
			}
			_ = json.Unmarshal([]byte(argsJSON), &a)
			return skill.ExecutionResult{Status: skill.StatusOK, Data: map[string]any{"echo": a.Msg}, Summary: "echoed " + a.Msg}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	llm := &scriptedLLM{script: []string{
		`TOOL:echo:{"msg":"hi"}`,
		"FINAL:已收到：hi",
	}}
	sink := &agent.MemSink{}
	res, err := (&agent.Runner{}).Run(context.Background(), agent.RunRequest{
		TraceID: "trace-2", UserID: "u1", UserContent: "echo hi",
		Registry: registry, LLM: llm,
		Config: agent.DefaultConfig(), Logger: zerolog.Nop(),
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.FinalContent, "已收到") {
		t.Fatalf("final = %q", res.FinalContent)
	}

	// Verify the event stream shape.
	got := map[string]int{}
	for _, ev := range sink.Events() {
		got[ev.Type]++
	}
	if got["step"] < 2 {
		t.Fatalf("step count = %d (want >= 2)", got["step"])
	}
	if got["tool_call"] != 1 {
		t.Fatalf("tool_call count = %d", got["tool_call"])
	}
	if got["tool_result"] != 1 {
		t.Fatalf("tool_result count = %d", got["tool_result"])
	}
	if got["final"] != 1 {
		t.Fatalf("final count = %d", got["final"])
	}
	if llm.calls != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", llm.calls)
	}
}

// TestAgentPendingHumanExitsEarly verifies the loop terminates as soon as
// a skill returns pending_human.
func TestAgentPendingHumanExitsEarly(t *testing.T) {
	registry := skill.NewRegistry()
	if err := registry.Register(skill.Definition{
		Name: "needs_confirm", Description: "needs human",
		ParametersJSON: `{"type":"object"}`, ReadOnly: false, Enabled: true,
		Execute: func(_ context.Context, _ string) (skill.ExecutionResult, error) {
			return skill.ExecutionResult{
				Status:        skill.StatusPendingHuman,
				PendingTicket: "ticket-xyz",
				Data:          map[string]any{"summary": "请确认退款"},
				Summary:       "请确认退款",
			}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	llm := &scriptedLLM{script: []string{`TOOL:needs_confirm:{}`}}
	sink := &agent.MemSink{}
	res, err := (&agent.Runner{}).Run(context.Background(), agent.RunRequest{
		TraceID: "trace-3", UserID: "u1", UserContent: "申请退款",
		Registry: registry, LLM: llm,
		Config: agent.DefaultConfig(), Logger: zerolog.Nop(),
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if res.HandedOver {
		t.Fatal("pending_human should not be a handover")
	}
	if !strings.Contains(res.FinalContent, "请确认") {
		t.Fatalf("final = %q", res.FinalContent)
	}

	got := map[string]int{}
	for _, ev := range sink.Events() {
		got[ev.Type]++
	}
	if got["pending_human"] != 1 {
		t.Fatalf("pending_human count = %d", got["pending_human"])
	}
	if got["final"] != 0 {
		t.Fatalf("final count = %d (should not produce final after pending_human)", got["final"])
	}
}

// TestAgentUnknownSkillErrorContinues verifies a tool error becomes a
// tool_result and the agent hands over on the next step (no final).
func TestAgentUnknownSkillErrorContinues(t *testing.T) {
	registry := skill.NewRegistry() // empty → all tools unknown
	llm := &scriptedLLM{script: []string{
		`TOOL:nonexistent:{"x":1}`,
		`TOOL:nonexistent:{"x":2}`, // LLM keeps trying → exhausts steps
		`TOOL:nonexistent:{"x":3}`,
		`TOOL:nonexistent:{"x":4}`,
		`TOOL:nonexistent:{"x":5}`,
	}}
	sink := &agent.MemSink{}
	cfg := agent.DefaultConfig()
	cfg.MaxSteps = 3 // limit so we hit max_steps, not just give up
	res, err := (&agent.Runner{}).Run(context.Background(), agent.RunRequest{
		TraceID: "trace-4", UserID: "u1", UserContent: "x",
		Registry: registry, LLM: llm, Config: cfg, Logger: zerolog.Nop(),
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if !res.HandedOver || res.HandoverReason != "max_steps" {
		t.Fatalf("expected max_steps handover, got handed=%v reason=%q", res.HandedOver, res.HandoverReason)
	}
}

// TestAgentLLMErrorHandsOver verifies an LLM transport failure falls back
// to handover with reason "llm_error".
func TestAgentLLMErrorHandsOver(t *testing.T) {
	registry := skill.NewRegistry()
	llm := &scriptedLLM{script: []string{"ERR:upstream timeout"}}
	sink := &agent.MemSink{}
	res, err := (&agent.Runner{}).Run(context.Background(), agent.RunRequest{
		TraceID: "trace-5", UserID: "u1", UserContent: "x",
		Registry: registry, LLM: llm, Config: agent.DefaultConfig(), Logger: zerolog.Nop(),
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if !res.HandedOver || res.HandoverReason != "llm_error" {
		t.Fatalf("expected llm_error handover, got %+v", res)
	}
}