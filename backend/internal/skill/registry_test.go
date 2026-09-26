package skill_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"intelligent_customer/backend/internal/skill"
	"intelligent_customer/backend/internal/testutil"
)

func TestRegistryRegisterBuiltin(t *testing.T) {
	r := skill.NewRegistry()
	if err := skill.RegisterBuiltins(r, &skill.MockData{}); err != nil {
		t.Fatalf("register builtins: %v", err)
	}
	names := r.Names()
	want := map[string]bool{"query_order": true, "query_coupon": true, "apply_refund": true}
	if len(names) != len(want) {
		t.Fatalf("got %d skills, want %d (%v)", len(names), len(want), names)
	}
	for _, n := range names {
		if !want[n] {
			t.Fatalf("unexpected skill: %s", n)
		}
	}
}

func TestRegistryRejectsDuplicate(t *testing.T) {
	r := skill.NewRegistry()
	if err := r.Register(skill.Definition{Name: "x", Execute: func(context.Context, string) (skill.ExecutionResult, error) {
		return skill.ExecutionResult{Status: skill.StatusOK}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(skill.Definition{Name: "x", Execute: func(context.Context, string) (skill.ExecutionResult, error) {
		return skill.ExecutionResult{Status: skill.StatusOK}, nil
	}}); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestRegistryDynamicReadOnlyEnforced(t *testing.T) {
	r := skill.NewRegistry()
	mut := skill.Definition{
		Name:     "mut",
		ReadOnly: false,
		Execute:  func(context.Context, string) (skill.ExecutionResult, error) { return skill.ExecutionResult{Status: skill.StatusOK}, nil },
	}
	if err := r.UpsertDynamic(mut, "db"); err == nil {
		t.Fatal("expected dynamic non-readonly to be rejected")
	}
	ro := mut
	ro.Name = "ro"
	ro.ReadOnly = true
	if err := r.UpsertDynamic(ro, "db"); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryDisableBuiltinRefused(t *testing.T) {
	r := skill.NewRegistry()
	if err := skill.RegisterBuiltins(r, &skill.MockData{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Disable("query_order"); err == nil {
		t.Fatal("expected disable of builtin to be refused")
	}
}

func TestQueryOrderMock(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	ctx := context.Background()
	if err := skill.SeedMockData(ctx, conn, "u-1"); err != nil {
		t.Fatal(err)
	}

	r := skill.NewRegistry()
	if err := skill.RegisterBuiltins(r, &skill.MockData{DB: conn, TicketTTL: time.Minute}); err != nil {
		t.Fatal(err)
	}

	d, err := r.Get("query_order")
	if err != nil {
		t.Fatal(err)
	}
	res, err := d.Execute(ctx, `{"order_id":"ORD-1001","user_id":"u-1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != skill.StatusOK {
		t.Fatalf("status = %s", res.Status)
	}
	if got := res.Data["status"]; got != "shipped" {
		t.Fatalf("status = %v", got)
	}

	// Permission check: a different user gets redacted.
	res2, err := d.Execute(ctx, `{"order_id":"ORD-1001","user_id":"u-other"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Data["status"] != "permission_denied" {
		t.Fatalf("expected permission_denied, got %v", res2.Data["status"])
	}

	// Missing order returns not_found without error.
	res3, err := d.Execute(ctx, `{"order_id":"NOPE","user_id":"u-1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res3.Data["status"] != "not_found" {
		t.Fatalf("expected not_found, got %v", res3.Data["status"])
	}
}

func TestQueryCouponMock(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	ctx := context.Background()
	if err := skill.SeedMockData(ctx, conn, "u-1"); err != nil {
		t.Fatal(err)
	}
	r := skill.NewRegistry()
	if err := skill.RegisterBuiltins(r, &skill.MockData{DB: conn}); err != nil {
		t.Fatal(err)
	}
	d, _ := r.Get("query_coupon")
	res, err := d.Execute(ctx, `{"user_id":"u-1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != skill.StatusOK {
		t.Fatalf("status = %s", res.Status)
	}
	coupons, ok := res.Data["coupons"].([]map[string]any)
	if !ok || len(coupons) == 0 {
		t.Fatalf("expected coupons, got %v", res.Data)
	}
	// EXPIRED coupon has expires_at in the past; the query filters it.
	for _, c := range coupons {
		if c["code"] == "EXPIRED" {
			t.Fatal("EXPIRED coupon should have been filtered out")
		}
	}
	if pts, _ := res.Data["points"].(int64); pts != 2480 {
		t.Fatalf("points = %v, want 2480", res.Data["points"])
	}
}

func TestApplyRefundPendingHuman(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	ctx := context.Background()
	if err := skill.SeedMockData(ctx, conn, "u-1"); err != nil {
		t.Fatal(err)
	}
	r := skill.NewRegistry()
	if err := skill.RegisterBuiltins(r, &skill.MockData{DB: conn, TicketTTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	d, _ := r.Get("apply_refund")
	res, err := d.Execute(ctx, `{"order_id":"ORD-1001","reason":"不想要了","amount_cents":5000,"user_id":"u-1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != skill.StatusPendingHuman {
		t.Fatalf("status = %s, want pending_human", res.Status)
	}
	if res.PendingTicket == "" {
		t.Fatal("ticket id missing")
	}

	// Confirm via tickets repo.
	tk, err := skill.NewTickets(conn).Confirm(ctx, res.PendingTicket, "u-1")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != "confirmed" {
		t.Fatalf("ticket status = %s", tk.Status)
	}

	// Order should now be in "refunding" status.
	var status string
	if err := conn.QueryRowContext(ctx, `SELECT status FROM mock_orders WHERE order_id='ORD-1001'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "refunding" {
		t.Fatalf("order status = %s, want refunding", status)
	}
}

func TestApplyRefundRejectsWrongUser(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	ctx := context.Background()
	if err := skill.SeedMockData(ctx, conn, "u-1"); err != nil {
		t.Fatal(err)
	}
	r := skill.NewRegistry()
	if err := skill.RegisterBuiltins(r, &skill.MockData{DB: conn, TicketTTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	d, _ := r.Get("apply_refund")
	_, err := d.Execute(ctx, `{"order_id":"ORD-1001","reason":"x","amount_cents":100,"user_id":"u-other"}`)
	if err == nil || !strings.Contains(err.Error(), "not belong") {
		t.Fatalf("expected ownership error, got %v", err)
	}
}

func TestApplyRefundRejectsOverAmount(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	ctx := context.Background()
	if err := skill.SeedMockData(ctx, conn, "u-1"); err != nil {
		t.Fatal(err)
	}
	r := skill.NewRegistry()
	if err := skill.RegisterBuiltins(r, &skill.MockData{DB: conn, TicketTTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	d, _ := r.Get("apply_refund")
	res, err := d.Execute(ctx, `{"order_id":"ORD-1001","reason":"x","amount_cents":99999999,"user_id":"u-1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != skill.StatusOK {
		t.Fatalf("status = %s, want ok (over-limit rejected with soft fail)", res.Status)
	}
	if res.Data["status"] != "rejected" {
		t.Fatalf("expected rejected, got %v", res.Data["status"])
	}
}

func TestLoadFromFS(t *testing.T) {
	dir := t.TempDir()
	// Write a JSON skill file with a builtin-style echo handler.
	if err := writeFile(t, filepath.Join(dir, "echo_skill.json"), `{
		"name":"echo_skill",
		"description":"echo back the args",
		"category":"general",
		"parametersJson":"{\"type\":\"object\",\"properties\":{\"msg\":{\"type\":\"string\"}}}",
		"handlerKind":"echo",
		"handlerConfig":"{}",
		"enabled":true,
		"requiresHuman":false,
		"readOnly":true
	}`); err != nil {
		t.Fatal(err)
	}
	r := skill.NewRegistry()
	n, err := r.LoadFromFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("loaded %d", n)
	}
	d, err := r.Get("echo_skill")
	if err != nil {
		t.Fatal(err)
	}
	res, err := d.Execute(context.Background(), `{"msg":"hi"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != skill.StatusOK {
		t.Fatalf("status = %s", res.Status)
	}
}

func TestSnapshotMetaSorted(t *testing.T) {
	r := skill.NewRegistry()
	if err := r.Register(skill.Definition{Name: "z", Execute: func(context.Context, string) (skill.ExecutionResult, error) {
		return skill.ExecutionResult{Status: skill.StatusOK}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(skill.Definition{Name: "a", Execute: func(context.Context, string) (skill.ExecutionResult, error) {
		return skill.ExecutionResult{Status: skill.StatusOK}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	meta := r.SnapshotMeta()
	if meta[0].Name != "a" || meta[1].Name != "z" {
		t.Fatalf("not sorted: %+v", meta)
	}
}

func writeFile(t *testing.T, path, content string) error {
	t.Helper()
	return writeFileBytes(path, []byte(content))
}

func writeFileBytes(path string, b []byte) error {
	// Avoid pulling io/ioutil — use os directly.
	return osWriteFile(path, b, 0o644)
}