package jev

import (
	"context"
	"strings"
	"testing"
	"time"

	"intelligent_customer/backend/internal/tenant"
	"intelligent_customer/backend/internal/testutil"
)

func TestTemplateRepo_CreateAndGet(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewTemplateRepo(conn)
	ctx := context.Background()

	rec := &TemplateRecord{
		TenantID:     tenant.DefaultID,
		Name:         "intent_routing",
		Version:      2,
		Trigger:      TriggerMessageEntry,
		OutputType:   OutputChoice,
		Labels:       []string{"order", "refund"},
		Instructions: "pick one",
		Fallback:     FallbackSpec{Type: "default", Value: "chitchat"},
		DefinitionYAML: "yaml blob",
		Status:       "draft",
	}
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.ID == "" || rec.CreatedAt.IsZero() {
		t.Fatal("Create must populate ID + CreatedAt")
	}
	got, err := repo.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != rec.Name || got.Version != 2 || len(got.Labels) != 2 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Fallback.Value != "chitchat" {
		t.Fatalf("fallback not persisted: %+v", got.Fallback)
	}
}

func TestTemplateRepo_VersionConflict(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewTemplateRepo(conn)
	ctx := context.Background()

	mk := func(v int) *TemplateRecord {
		return &TemplateRecord{
			TenantID: tenant.DefaultID, Name: "x", Version: v,
			Trigger: TriggerMessageEntry, OutputType: OutputChoice,
			Labels: []string{"a"}, Instructions: "x",
		}
	}
	if err := repo.Create(ctx, mk(1)); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := repo.Create(ctx, mk(1))
	if err == nil || !strings.Contains(err.Error(), "version conflict") {
		t.Fatalf("expected version conflict, got %v", err)
	}
}

func TestTemplateRepo_PublishAndArchive(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewTemplateRepo(conn)
	ctx := context.Background()
	rec := &TemplateRecord{
		TenantID: tenant.DefaultID, Name: "x", Version: 1,
		Trigger: TriggerMessageEntry, OutputType: OutputChoice,
		Labels: []string{"a"}, Instructions: "x",
	}
	if err := repo.Create(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := repo.Publish(ctx, rec.ID); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got, _ := repo.Get(ctx, rec.ID)
	if got.Status != "published" || got.PublishedAt == nil {
		t.Fatalf("Publish didn't flip state: %+v", got)
	}
	if err := repo.Archive(ctx, rec.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	got, _ = repo.Get(ctx, rec.ID)
	if got.Status != "archived" || got.ArchivedAt == nil {
		t.Fatalf("Archive didn't flip state: %+v", got)
	}
}

func TestTemplateRepo_PublishMissingReturnsErr(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewTemplateRepo(conn)
	if err := repo.Publish(context.Background(), "ghost"); err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestTemplateRepo_DeleteAndList(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewTemplateRepo(conn)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		if err := repo.Create(ctx, &TemplateRecord{
			TenantID: tenant.DefaultID, Name: "tpl", Version: i,
			Trigger: TriggerMessageEntry, OutputType: OutputChoice,
			Labels: []string{"a"}, Instructions: "x",
		}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := repo.ListByTenant(ctx, tenant.DefaultID)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3, got %d", len(out))
	}
	if err := repo.Delete(ctx, out[0].ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	out2, _ := repo.ListByTenant(ctx, tenant.DefaultID)
	if len(out2) != 2 {
		t.Fatalf("expected 2 after delete, got %d", len(out2))
	}
}

// ----- DecisionRepo ---------------------------------------------------------

func TestDecisionRepo_InsertAndGet(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewDecisionRepo(conn)
	ctx := context.Background()

	conf := 0.92
	d := &DecisionRecord{
		TenantID:     tenant.DefaultID,
		TemplateName: "intent_routing",
		Trigger:      TriggerMessageEntry,
		InputHash:    "abcd",
		InputJSON:    `{"message":"hi"}`,
		OutputJSON:   `{"choice":"refund","score":0.92}`,
		Fallback:     false,
		Confidence:   &conf,
		LatencyMS:    42,
		Status:       "decided",
		TraceID:      "trace-1",
	}
	if err := repo.Insert(ctx, d); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := repo.Get(ctx, d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TemplateName != "intent_routing" || got.LatencyMS != 42 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Confidence == nil || *got.Confidence != 0.92 {
		t.Fatalf("confidence lost: %v", got.Confidence)
	}
}

func TestDecisionRepo_ListAndFilter(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewDecisionRepo(conn)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := repo.Insert(ctx, &DecisionRecord{
			TenantID:     tenant.DefaultID,
			TemplateName: "intent_routing",
			Trigger:      TriggerMessageEntry,
			InputHash:    "h", InputJSON: "{}", OutputJSON: "{}",
			LatencyMS: i * 10,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Insert(ctx, &DecisionRecord{
		TenantID: tenant.DefaultID, TemplateName: "sensitive_check",
		Trigger:  TriggerPreIngest,
		InputHash: "h2", InputJSON: "{}", OutputJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	all, total, err := repo.List(ctx, DecisionFilter{TenantID: tenant.DefaultID, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 6 || len(all) != 6 {
		t.Fatalf("expected 6, got %d (total=%d)", len(all), total)
	}
	scoped, _, err := repo.List(ctx, DecisionFilter{
		TenantID: tenant.DefaultID, TemplateName: "intent_routing", Limit: 10,
	})
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}
	if len(scoped) != 5 {
		t.Fatalf("expected 5 intent_routing decisions, got %d", len(scoped))
	}
}

func TestDecisionRepo_MarkReviewed(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewDecisionRepo(conn)
	ctx := context.Background()
	d := &DecisionRecord{
		TenantID: tenant.DefaultID, TemplateName: "x", Trigger: TriggerMessageEntry,
		InputHash: "h", InputJSON: "{}", OutputJSON: "{}",
	}
	if err := repo.Insert(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkReviewed(ctx, d.ID, "accepted", `{"label":"refund"}`, "ops"); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}
	got, _ := repo.Get(ctx, d.ID)
	if got.Status != "accepted" || got.GroundTruth == "" || got.ReviewedBy != "ops" {
		t.Fatalf("MarkReviewed didn't update: %+v", got)
	}
	if got.ReviewedAt == nil {
		t.Fatal("ReviewedAt should be stamped")
	}
}

func TestDecisionRepo_MarkReviewedRejectsBadStatus(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewDecisionRepo(conn)
	if err := repo.MarkReviewed(context.Background(), "any", "weird", "", "ops"); err == nil {
		t.Fatal("expected bad-request error")
	}
}

func TestDecisionRepo_Stats(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewDecisionRepo(conn)
	ctx := context.Background()
	// 3 success + 2 fallback
	for i := 0; i < 5; i++ {
		if err := repo.Insert(ctx, &DecisionRecord{
			TenantID: tenant.DefaultID, TemplateName: "intent_routing",
			Trigger: TriggerMessageEntry,
			InputHash: "h", InputJSON: "{}", OutputJSON: "{}",
			LatencyMS: (i + 1) * 10,
			Fallback:  i >= 3,
		}); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := repo.Stats(ctx, tenant.DefaultID, 24*time.Hour)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Total != 5 {
		t.Fatalf("expected total=5, got %d", stats.Total)
	}
	if len(stats.ByTemplate) != 1 {
		t.Fatalf("expected 1 template row, got %d", len(stats.ByTemplate))
	}
	if stats.ByTemplate[0].Fallback != 2 {
		t.Fatalf("expected fallback=2, got %d", stats.ByTemplate[0].Fallback)
	}
	if stats.P95LatencyMS == 0 {
		t.Fatal("P95 should be non-zero with data")
	}
}
