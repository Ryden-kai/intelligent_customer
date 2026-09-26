// Package service — agent_chat_test.go
//
// Integration tests for the v2.1.1 Jev trigger points wired into
// service.AgentChat. The service has three hook points:
//
//   - TriggerMessageEntry  → informational only (intent logging)
//   - TriggerPreIngest     → blocks + handover on "violate"
//   - TriggerPreReply      → informational + audit row
//
// We exercise the service-level helpers (fireJevEntry / fireJevPreIngest
// / fireJevPreReply) with a real Registry + Orchestrator + DecisionRepo
// against an in-memory SQLite. The agent loop is stubbed so tests don't
// hit the network and we can isolate the Jev paths.

package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/agent"
	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/llm"
	"intelligent_customer/backend/internal/repo"
	"intelligent_customer/backend/internal/skill"
	"intelligent_customer/backend/internal/tenant"
	"intelligent_customer/backend/internal/testutil"
)

// stubToolLLM is a deterministic ToolChatCompleter that returns an
// empty tool list so the agent loop terminates after one step.
type stubToolLLM struct{}

func (s *stubToolLLM) Chat(_ context.Context, _ string, _ []llm.Message) (string, error) {
	return "ok", nil
}
func (s *stubToolLLM) ToolChat(_ context.Context, _ string, _ []llm.Message, _ []llm.ToolDef) (llm.ToolChatResult, error) {
	return llm.ToolChatResult{Content: "stub reply", ToolCalls: nil}, nil
}
func (s *stubToolLLM) Identity() llm.Model { return llm.Model{Provider: "stub", Name: "stub"} }

// fakeJevClient lets each test pre-program the live verdict per
// trigger. Mirrors jev.LiveClient without dragging in the HTTP
// transport.
type fakeJevClient struct {
	decisions map[jev.TriggerPoint]*jev.Decision
	errByTrig map[jev.TriggerPoint]error
}

func (f *fakeJevClient) Enabled() bool { return true }
func (f *fakeJevClient) Decide(_ context.Context, _ *jev.Template, req jev.DecisionRequest) (*jev.Decision, error) {
	if err, ok := f.errByTrig[req.Trigger]; ok && err != nil {
		return nil, err
	}
	if d, ok := f.decisions[req.Trigger]; ok {
		// Stamp the request fields so callers can introspect.
		out := *d
		out.TemplateName = "test"
		out.TemplateVersion = 1
		out.Trigger = req.Trigger
		out.TenantID = req.TenantID
		return &out, nil
	}
	return &jev.Decision{Choice: "ok", Confidence: ptrFloat(0.5)}, nil
}

func ptrFloat(v float64) *float64 { return &v }

// newAgentChatJev wires an AgentChat service backed by an in-memory DB
// + stub LLM + Orchestrator driven by the supplied fake Jev client.
// The orchestrator's templates are preloaded with the v2.1 defaults so
// TriggerPreIngest / TriggerMessageEntry / TriggerPreReply all hit a
// real Registry.Get() instead of falling back to the empty-template path.
func newAgentChatJev(t *testing.T, fake *fakeJevClient) (*AgentChat, *jev.DecisionRepo, func()) {
	t.Helper()
	conn, cleanup := testutil.OpenTempSQLite(t)
	convs := repo.NewConversations(conn)
	msgs := repo.NewMessages(conn)
	feedbacks := repo.NewFeedback(conn)
	registry := skill.NewRegistry()
	mock := &skill.MockData{DB: conn, TicketTTL: 5 * 60 * 1e9} // 5 min
	if err := skill.RegisterBuiltins(registry, mock); err != nil {
		t.Fatalf("register builtins: %v", err)
	}

	decisionsRepo := jev.NewDecisionRepo(conn)
	jevReg := jev.NewRegistry()
	orchestrator := jev.NewOrchestrator(jev.Options{
		Registry: jevReg,
		Client:   fake,
		Fallback: &stubFallback{},
		Repo:     decisionsRepo,
		Logger:   zerolog.Nop(),
	})

	block := true
	svc := &AgentChat{
		Convs:            convs,
		Msgs:             msgs,
		Feedbacks:        feedbacks,
		Registry:         registry,
		Invocations:      skill.NewInvocations(conn),
		Tickets:          skill.NewTickets(conn),
		LLM:              &stubToolLLM{},
		HandoverCfg:      HandoverConfig{},
		Cfg:              agent.DefaultConfig(),
		Logger:           testutil.QuietLogger(),
		JEV:              orchestrator,
		BlockOnSensitive: &block,
	}
	return svc, decisionsRepo, cleanup
}

// stubFallback is a minimal Fallback that always returns "clean" so the
// service never short-circuits via the no-template path.
type stubFallback struct{}

func (s *stubFallback) Handle(_ context.Context, _ *jev.Template, req jev.DecisionRequest, _ error) (*jev.Decision, error) {
	return &jev.Decision{Choice: "fallback_clean", Fallback: true}, nil
}

// TestAgentChat_JevEntryDecisionPersisted asserts that the message.entry
// trigger fires, persists a decision row, and never blocks the chat.
func TestAgentChat_JevEntryDecisionPersisted(t *testing.T) {
	fake := &fakeJevClient{
		decisions: map[jev.TriggerPoint]*jev.Decision{
			jev.TriggerMessageEntry: {Choice: "order", Confidence: ptrFloat(0.91)},
			jev.TriggerPreIngest:    {Choice: "clean", Confidence: ptrFloat(0.99)},
			jev.TriggerPreReply:     {Choice: "clean", Confidence: ptrFloat(0.99)},
		},
	}
	svc, decisionsRepo, cleanup := newAgentChatJev(t, fake)
	defer cleanup()

	res, err := svc.Handle(context.Background(), ServeRequest{
		UserID:  "u1",
		Content: "查订单 ORD-1001",
	}, nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.HandedOver {
		t.Fatal("entry trigger must not cause handover")
	}
	if res.Source == "" {
		t.Fatal("expected non-empty source from agent loop")
	}
	// Two decisions persisted: message.entry + message.pre_ingest.
	// pre_reply also fires after the agent loop completes (see
	// fireJevPreReply) — so we expect at least 2 rows.
	rows, total, err := decisionsRepo.List(context.Background(), jev.DecisionFilter{TenantID: tenant.DefaultID})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total < 2 {
		t.Fatalf("expected at least 2 decision rows, got %d", total)
	}
	found := map[jev.TriggerPoint]bool{}
	for _, r := range rows {
		found[r.Trigger] = true
	}
	for _, want := range []jev.TriggerPoint{jev.TriggerMessageEntry, jev.TriggerPreIngest} {
		if !found[want] {
			t.Fatalf("missing decision row for trigger %s", want)
		}
	}
}

// TestAgentChat_JevViolateTriggersHandover asserts that the message
// pre_ingest "violate" verdict blocks the message, marks the
// conversation handed over, and persists a system reply that mentions
// the content-safety policy.
func TestAgentChat_JevViolateTriggersHandover(t *testing.T) {
	fake := &fakeJevClient{
		decisions: map[jev.TriggerPoint]*jev.Decision{
			jev.TriggerPreIngest: {Choice: "violate", Confidence: ptrFloat(0.88)},
		},
	}
	svc, _, cleanup := newAgentChatJev(t, fake)
	defer cleanup()

	res, err := svc.Handle(context.Background(), ServeRequest{
		UserID:  "u2",
		Content: "some disallowed content",
	}, nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !res.HandedOver {
		t.Fatal("violate verdict must cause handover")
	}
	if res.HandoverReason != "jev_violate" {
		t.Fatalf("expected handover reason 'jev_violate', got %q", res.HandoverReason)
	}
	if !strings.Contains(res.AssistantMessage.Content, "敏感") && !strings.Contains(res.AssistantMessage.Content, "人工") {
		t.Fatalf("handover reply should explain the block + mention human review, got %q", res.AssistantMessage.Content)
	}
}

// TestAgentChat_JevCleanContinues proves that a "clean" pre_ingest
// verdict lets the agent loop run as usual — no spurious handover.
func TestAgentChat_JevCleanContinues(t *testing.T) {
	fake := &fakeJevClient{
		decisions: map[jev.TriggerPoint]*jev.Decision{
			jev.TriggerPreIngest: {Choice: "clean", Confidence: ptrFloat(0.99)},
		},
	}
	svc, _, cleanup := newAgentChatJev(t, fake)
	defer cleanup()

	res, err := svc.Handle(context.Background(), ServeRequest{
		UserID:  "u3",
		Content: "你好",
	}, nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.HandedOver {
		t.Fatal("clean verdict must NOT cause handover")
	}
}

// TestAgentChat_JevSensitiveNoBlock asserts that a "sensitive"
// (but non-violate) verdict doesn't block — the chat should fall
// through to the agent loop. Operators can react to sensitive
// separately via Jev Observability.
func TestAgentChat_JevSensitiveNoBlock(t *testing.T) {
	fake := &fakeJevClient{
		decisions: map[jev.TriggerPoint]*jev.Decision{
			jev.TriggerPreIngest: {Choice: "sensitive", Confidence: ptrFloat(0.6)},
		},
	}
	svc, _, cleanup := newAgentChatJev(t, fake)
	defer cleanup()

	res, err := svc.Handle(context.Background(), ServeRequest{
		UserID:  "u4",
		Content: "borderline content",
	}, nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.HandedOver {
		t.Fatal("sensitive verdict must NOT block; only 'violate' does")
	}
}

// TestAgentChat_JevDisabledService verifies the chat still works when
// no Orchestrator is wired — the old behaviour is preserved.
func TestAgentChat_JevDisabledService(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	convs := repo.NewConversations(conn)
	msgs := repo.NewMessages(conn)
	registry := skill.NewRegistry()
	mock := &skill.MockData{DB: conn, TicketTTL: 5 * 60 * 1e9}
	if err := skill.RegisterBuiltins(registry, mock); err != nil {
		t.Fatalf("register builtins: %v", err)
	}
	svc := &AgentChat{
		Convs:       convs,
		Msgs:        msgs,
		Feedbacks:   repo.NewFeedback(conn),
		Registry:    registry,
		Invocations: skill.NewInvocations(conn),
		Tickets:     skill.NewTickets(conn),
		LLM:         &stubToolLLM{},
		Cfg:         agent.DefaultConfig(),
		Logger:      testutil.QuietLogger(),
		// JEV intentionally nil
	}
	res, err := svc.Handle(context.Background(), ServeRequest{
		UserID: "u5", Content: "纯聊天",
	}, nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.HandedOver {
		t.Fatal("no orchestrator wired → no block possible")
	}
}

// TestAgentChat_JevBlockDisabled proves that setting BlockOnSensitive
// to false records the decision but never blocks.
func TestAgentChat_JevBlockDisabled(t *testing.T) {
	fake := &fakeJevClient{
		decisions: map[jev.TriggerPoint]*jev.Decision{
			jev.TriggerPreIngest: {Choice: "violate", Confidence: ptrFloat(0.99)},
		},
	}
	svc, decisionsRepo, cleanup := newAgentChatJev(t, fake)
	defer cleanup()
	block := false
	svc.BlockOnSensitive = &block

	res, err := svc.Handle(context.Background(), ServeRequest{
		UserID:  "u6",
		Content: "blocked content",
	}, nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.HandedOver {
		t.Fatal("block disabled → must not handover despite violate")
	}
	// But the verdict should still be persisted for observability.
	rows, _, err := decisionsRepo.List(context.Background(), jev.DecisionFilter{TenantID: tenant.DefaultID})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.Trigger == jev.TriggerPreIngest {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected pre_ingest decision persisted even when block disabled")
	}
}

// TestAgentChat_JevErrorFallback proves that a Jev upstream error does
// not crash the chat — the orchestrator's fallback path returns a
// clean default and the agent loop continues.
func TestAgentChat_JevErrorFallback(t *testing.T) {
	upErr := &fakeJevClient{
		errByTrig: map[jev.TriggerPoint]error{
			jev.TriggerPreIngest:    context.DeadlineExceeded,
			jev.TriggerMessageEntry: context.DeadlineExceeded,
		},
	}
	svc, _, cleanup := newAgentChatJev(t, upErr)
	defer cleanup()

	res, err := svc.Handle(context.Background(), ServeRequest{
		UserID: "u7", Content: "live down",
	}, nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.HandedOver {
		t.Fatal("Jev error must not cause handover; fallback returns non-violate")
	}
}

// routerFakeChannel mirrors llm.Channel for tests. Lets us verify the
// Router used by AgentChat actually fails over when the primary is
// down — without dragging in HTTP.
type routerFakeChannel struct {
	id     string
	err    error
	calls  int
	reply  string
	toolOK string
}

func (f *routerFakeChannel) Chat(_ context.Context, _ string, _ []llm.Message) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.reply, nil
}
func (f *routerFakeChannel) ToolChat(_ context.Context, _ string, _ []llm.Message, _ []llm.ToolDef) (llm.ToolChatResult, error) {
	f.calls++
	if f.err != nil {
		return llm.ToolChatResult{}, f.err
	}
	return llm.ToolChatResult{Content: f.toolOK}, nil
}
func (f *routerFakeChannel) Identity() llm.Model { return llm.Model{Provider: "fake", Name: f.id} }

// TestAgentChat_RouterFallbackOnPrimaryError wires the AgentChat LLM
// field to a 2-channel Router, breaks the primary, and verifies the
// agent loop still completes via the secondary. This is the v2.1.1
// "Router 接 AgentChat" deliverable: the Router is the only LLM the
// service knows about.
func TestAgentChat_RouterFallbackOnPrimaryError(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()

	primary := &routerFakeChannel{id: "primary-down", err: errors.New("primary 502")}
	secondary := &routerFakeChannel{id: "secondary-ok", reply: "secondary says hi", toolOK: "via-secondary"}
	router := llm.NewRouter([]llm.ChannelConfig{
		{Slot: "primary", Provider: "openai", Channel: primary},
		{Slot: "secondary", Provider: "minimax", Channel: secondary, Regions: []string{"cn"}},
	}, zerolog.Nop())

	registry := skill.NewRegistry()
	mock := &skill.MockData{DB: conn, TicketTTL: 5 * 60 * 1e9}
	if err := skill.RegisterBuiltins(registry, mock); err != nil {
		t.Fatalf("register builtins: %v", err)
	}
	svc := &AgentChat{
		Convs:       repo.NewConversations(conn),
		Msgs:        repo.NewMessages(conn),
		Feedbacks:   repo.NewFeedback(conn),
		Registry:    registry,
		Invocations: skill.NewInvocations(conn),
		Tickets:     skill.NewTickets(conn),
		LLM:         router,
		Cfg:         agent.DefaultConfig(),
		Logger:      testutil.QuietLogger(),
	}

	res, err := svc.Handle(context.Background(), ServeRequest{
		UserID: "ur1", Content: "测试 fallback",
	}, nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.HandedOver {
		t.Fatalf("fallback should not trigger handover, got reason=%q", res.HandoverReason)
	}
	if primary.calls == 0 {
		t.Fatal("primary should have been attempted")
	}
	if secondary.calls == 0 {
		t.Fatal("secondary should have been attempted after primary error")
	}
	if !strings.Contains(res.AssistantMessage.Content, "via-secondary") {
		t.Fatalf("expected secondary reply, got %q", res.AssistantMessage.Content)
	}
}