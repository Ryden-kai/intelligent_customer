package jev

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/tenant"
	"intelligent_customer/backend/internal/testutil"
)

type fakeClient struct {
	enabled bool
	decide  func(ctx context.Context, tpl *Template, req DecisionRequest) (*Decision, error)
}

func (f *fakeClient) Enabled() bool { return f.enabled }
func (f *fakeClient) Decide(ctx context.Context, tpl *Template, req DecisionRequest) (*Decision, error) {
	if f.decide == nil {
		return &Decision{Choice: "ok", Confidence: ptrFloat(0.9)}, nil
	}
	return f.decide(ctx, tpl, req)
}

func ptrFloat(v float64) *float64 { return &v }

type fakeFallback struct {
	handle func(ctx context.Context, tpl *Template, req DecisionRequest, liveErr error) (*Decision, error)
}

func (f *fakeFallback) Handle(ctx context.Context, tpl *Template, req DecisionRequest, liveErr error) (*Decision, error) {
	if f.handle != nil {
		return f.handle(ctx, tpl, req, liveErr)
	}
	return &Decision{Choice: "fallback", Fallback: true, FallbackReason: "fake"}, nil
}

type fakeLoopback struct {
	mu      sync.Mutex
	called  int
	last    *Decision
	failErr error
}

func (f *fakeLoopback) Record(ctx context.Context, d *Decision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.called++
	dCopy := *d
	f.last = &dCopy
	return f.failErr
}

func newOrchestratorForTest(t *testing.T, c LiveClient, fb Fallback, repo *DecisionRepo, loop Loopback) (*Orchestrator, *Registry) {
	t.Helper()
	r := NewRegistry()
	o := NewOrchestrator(Options{
		Registry:       r,
		Client:         c,
		Fallback:       fb,
		Repo:           repo,
		Loopback:       loop,
		Logger:         zerolog.Nop(),
		DefaultTimeout: 500 * time.Millisecond,
	})
	return o, r
}

func TestOrchestrator_LiveCallSuccess(t *testing.T) {
	c := &fakeClient{enabled: true, decide: func(ctx context.Context, tpl *Template, req DecisionRequest) (*Decision, error) {
		return &Decision{Choice: "refund", Confidence: ptrFloat(0.92)}, nil
	}}
	loop := &fakeLoopback{}
	o, _ := newOrchestratorForTest(t, c, &fakeFallback{}, nil, loop)
	d, err := o.Decide(context.Background(), DecisionRequest{
		TenantID: tenant.DefaultID,
		Trigger:  TriggerMessageEntry,
		Input:    map[string]any{"message": "我要退款"},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if d.Choice != "refund" || d.Fallback {
		t.Fatalf("expected live decision, got %+v", d)
	}
	if d.LatencyMS < 0 {
		t.Fatal("LatencyMS should be set")
	}
}

func TestOrchestrator_LiveFailsFallbackTakesOver(t *testing.T) {
	c := &fakeClient{enabled: true, decide: func(ctx context.Context, tpl *Template, req DecisionRequest) (*Decision, error) {
		return nil, errors.New("jev upstream 500")
	}}
	loop := &fakeLoopback{}
	o, _ := newOrchestratorForTest(t, c, &fakeFallback{}, nil, loop)
	d, err := o.Decide(context.Background(), DecisionRequest{
		TenantID: tenant.DefaultID, Trigger: TriggerMessageEntry,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !d.Fallback || d.Choice != "fallback" {
		t.Fatalf("expected fallback decision, got %+v", d)
	}
	if loop.called != 1 {
		t.Fatalf("expected loopback called once, got %d", loop.called)
	}
}

func TestOrchestrator_LiveAndFallbackBothFail(t *testing.T) {
	c := &fakeClient{enabled: true, decide: func(ctx context.Context, tpl *Template, req DecisionRequest) (*Decision, error) {
		return nil, errors.New("live down")
	}}
	fb := &fakeFallback{handle: func(ctx context.Context, tpl *Template, req DecisionRequest, liveErr error) (*Decision, error) {
		return nil, ErrNoFallback
	}}
	o, _ := newOrchestratorForTest(t, c, fb, nil, nil)
	_, err := o.Decide(context.Background(), DecisionRequest{
		TenantID: tenant.DefaultID, Trigger: TriggerMessageEntry,
	})
	if err == nil {
		t.Fatal("expected error when both fail")
	}
}

func TestOrchestrator_MissingTemplateFallsBack(t *testing.T) {
	c := &fakeClient{enabled: true}
	r := NewRegistry()
	r.Reset() // no builtins → registry miss
	fb := &fakeFallback{}
	o := NewOrchestrator(Options{
		Registry: r, Client: c, Fallback: fb,
		Logger: zerolog.Nop(),
	})
	d, err := o.Decide(context.Background(), DecisionRequest{
		TenantID: tenant.DefaultID, Trigger: TriggerMessageEntry,
	})
	if err != nil {
		t.Fatalf("expected fallback to handle missing template, got err: %v", err)
	}
	if !d.Fallback {
		t.Fatal("expected fallback flag set when template missing")
	}
}

func TestOrchestrator_DisabledClientFallsBack(t *testing.T) {
	c := &fakeClient{enabled: false}
	o, _ := newOrchestratorForTest(t, c, &fakeFallback{}, nil, nil)
	d, err := o.Decide(context.Background(), DecisionRequest{
		TenantID: tenant.DefaultID, Trigger: TriggerMessageEntry,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !d.Fallback {
		t.Fatalf("expected fallback when client disabled, got %+v", d)
	}
}

func TestOrchestrator_PersistsDecisionRow(t *testing.T) {
	c := &fakeClient{enabled: true, decide: func(ctx context.Context, tpl *Template, req DecisionRequest) (*Decision, error) {
		return &Decision{Choice: "order", Confidence: ptrFloat(0.7)}, nil
	}}
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewDecisionRepo(conn)
	o, _ := newOrchestratorForTest(t, c, &fakeFallback{}, repo, nil)
	_, err := o.Decide(context.Background(), DecisionRequest{
		TenantID: tenant.DefaultID, Trigger: TriggerMessageEntry,
		Input: map[string]any{"message": "查订单"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	out, total, err := repo.List(context.Background(), DecisionFilter{TenantID: tenant.DefaultID})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(out) != 1 {
		t.Fatalf("expected 1 row, got %d", total)
	}
	if out[0].TemplateName != "intent_routing" {
		t.Fatalf("expected template name intent_routing, got %s", out[0].TemplateName)
	}
	if !strings.Contains(out[0].OutputJSON, `"choice":"order"`) {
		t.Fatalf("expected choice=order in OutputJSON, got %s", out[0].OutputJSON)
	}
}

func TestOrchestrator_HonorsTimeout(t *testing.T) {
	c := &fakeClient{enabled: true, decide: func(ctx context.Context, tpl *Template, req DecisionRequest) (*Decision, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	o, _ := newOrchestratorForTest(t, c, &fakeFallback{}, nil, nil)
	start := time.Now()
	d, err := o.Decide(context.Background(), DecisionRequest{
		TenantID: tenant.DefaultID, Trigger: TriggerMessageEntry,
		Timeout: 50 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("expected fallback success after timeout, got err: %v", err)
	}
	if !d.Fallback {
		t.Fatal("expected fallback flag after timeout")
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("orchestrator didn't honor timeout: %v", elapsed)
	}
}

func TestOrchestrator_DecideFromContextUsesTenantInfo(t *testing.T) {
	c := &fakeClient{enabled: true, decide: func(ctx context.Context, tpl *Template, req DecisionRequest) (*Decision, error) {
		return &Decision{Choice: "x"}, nil
	}}
	o, _ := newOrchestratorForTest(t, c, &fakeFallback{}, nil, nil)
	ctx := tenant.WithTenant(context.Background(), tenant.Info{ID: "tnt_x"})
	d, err := o.DecideFromContext(ctx, TriggerMessageEntry, map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if d.TenantID != "tnt_x" {
		t.Fatalf("expected tnt_x, got %s", d.TenantID)
	}
}
