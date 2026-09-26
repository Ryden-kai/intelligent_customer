package jev

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/tenant"
	"intelligent_customer/backend/internal/testutil"
)

func TestInMemoryLoopback_RecordAndSnapshot(t *testing.T) {
	l := NewInMemoryLoopback(0)
	for i := 0; i < 3; i++ {
		_ = l.Record(context.Background(), &Decision{DecisionID: "x", TenantID: "t1"})
	}
	got := l.Snapshot()
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
}

func TestInMemoryLoopback_RingBuffer(t *testing.T) {
	l := NewInMemoryLoopback(2)
	for i := 0; i < 5; i++ {
		_ = l.Record(context.Background(), &Decision{DecisionID: "x"})
	}
	got := l.Snapshot()
	if len(got) != 2 {
		t.Fatalf("expected ring buffer to keep 2, got %d", len(got))
	}
}

func TestInMemoryLoopback_NilDecisionIsNoOp(t *testing.T) {
	l := NewInMemoryLoopback(0)
	if err := l.Record(context.Background(), nil); err != nil {
		t.Fatalf("nil decision should not error: %v", err)
	}
	if len(l.Snapshot()) != 0 {
		t.Fatal("expected empty snapshot")
	}
}

func TestDBLoopback_NoOpInV2_1(t *testing.T) {
	l := NewDBLoopback(nil)
	if err := l.Record(context.Background(), &Decision{DecisionID: "x"}); err != nil {
		t.Fatalf("DBLoopback.Record should be no-op, got %v", err)
	}
}

func TestSatisfactionLoopback_AppliesAcceptedForHighRating(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewDecisionRepo(conn)
	ctx := context.Background()

	// Seed a "decided" decision.
	d := &DecisionRecord{
		TenantID: tenant.DefaultID, TemplateName: "intent_routing",
		Trigger: TriggerMessageEntry,
		InputHash: "h", InputJSON: "{}", OutputJSON: "{}",
		Status: "decided",
	}
	if err := repo.Insert(ctx, d); err != nil {
		t.Fatal(err)
	}

	sl := NewSatisfactionLoopback(conn, repo)
	if err := sl.ApplyRating(ctx, tenant.DefaultID, 5, "great"); err != nil {
		t.Fatalf("ApplyRating: %v", err)
	}
	got, _ := repo.Get(ctx, d.ID)
	if got.Status != "accepted" {
		t.Fatalf("expected accepted, got %s", got.Status)
	}
	if got.GroundTruth == "" || got.ReviewedBy != "satisfaction_loopback" {
		t.Fatalf("ground truth or reviewer missing: %+v", got)
	}
}

func TestSatisfactionLoopback_AppliesRejectedForLowRating(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewDecisionRepo(conn)
	ctx := context.Background()
	d := &DecisionRecord{
		TenantID: tenant.DefaultID, TemplateName: "x",
		Trigger: TriggerMessageEntry,
		InputHash: "h", InputJSON: "{}", OutputJSON: "{}",
	}
	if err := repo.Insert(ctx, d); err != nil {
		t.Fatal(err)
	}
	sl := NewSatisfactionLoopback(conn, repo)
	if err := sl.ApplyRating(ctx, tenant.DefaultID, 2, "bad"); err != nil {
		t.Fatalf("ApplyRating: %v", err)
	}
	got, _ := repo.Get(ctx, d.ID)
	if got.Status != "rejected" {
		t.Fatalf("expected rejected, got %s", got.Status)
	}
}

func TestSatisfactionLoopback_NoDecisionReturnsErr(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewDecisionRepo(conn)
	sl := NewSatisfactionLoopback(conn, repo)
	err := sl.ApplyRating(context.Background(), tenant.DefaultID, 4, "")
	if !errors.Is(err, ErrNoDecisionForTenant) {
		t.Fatalf("expected ErrNoDecisionForTenant, got %v", err)
	}
}

func TestSatisfactionLoopback_RejectsBadRating(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewDecisionRepo(conn)
	sl := NewSatisfactionLoopback(conn, repo)
	if err := sl.ApplyRating(context.Background(), tenant.DefaultID, 0, ""); err == nil {
		t.Fatal("expected error for rating=0")
	}
	if err := sl.ApplyRating(context.Background(), tenant.DefaultID, 6, ""); err == nil {
		t.Fatal("expected error for rating=6")
	}
}

func TestSatisfactionLoopback_NilSafe(t *testing.T) {
	var sl *SatisfactionLoopback
	if err := sl.ApplyRating(context.Background(), tenant.DefaultID, 4, ""); err != nil {
		t.Fatalf("nil loopback should be silent, got %v", err)
	}
}

func TestOrchestrator_DBLookback_NoOp(t *testing.T) {
	c := &fakeClient{enabled: true}
	repo := NewDecisionRepo(nil) // pure stub
	o := NewOrchestrator(Options{
		Registry: NewRegistry(), Client: c, Fallback: &fakeFallback{},
		Repo: nil, Loopback: NewDBLoopback(repo),
		Logger: zerolog.Nop(),
	})
	d, err := o.Decide(context.Background(), DecisionRequest{
		TenantID: tenant.DefaultID, Trigger: TriggerMessageEntry,
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d == nil {
		t.Fatal("expected non-nil decision")
	}
}
