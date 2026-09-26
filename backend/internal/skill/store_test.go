package skill_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/skill"
	"intelligent_customer/backend/internal/testutil"
)

// helper: insert a mock_order owned by userID in 'paid' state.
func insertPaidOrder(t *testing.T, ctx context.Context, db *sql.DB, orderID, userID string) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`INSERT INTO mock_orders(order_id, user_id, status, amount_cents, paid_at, created_at)
		 VALUES(?, ?, 'paid', 19900, ?, ?)`,
		orderID, userID, time.Now().UnixMilli(), time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("insert mock order: %v", err)
	}
}

// helper: insert a skill_pending_ticket. status/expiresAt are explicit so
// tests can fast-forward expiry without time.Sleep. conversation_id is
// left NULL (it's nullable in the migration and the agent back-fills it
// after the fact).
func insertTicket(t *testing.T, ctx context.Context, db *sql.DB, ticketID, userID, payload string, status string, expiresAt time.Time) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`INSERT INTO skill_pending_tickets(id, conversation_id, user_id, skill_name, payload_json, summary, status, expires_at, created_at)
		 VALUES(?, NULL, ?, 'apply_refund', ?, 'apply refund ticket', ?, ?, ?)`,
		ticketID, userID, payload, status, expiresAt.UnixMilli(), time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("insert ticket: %v", err)
	}
}

func TestTicketsConfirmHappyPath(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	r := skill.NewTickets(conn)
	ctx := context.Background()

	const (
		ticketID = "t-happy"
		orderID  = "ORD-7001"
		userID   = "u-7"
	)
	insertPaidOrder(t, ctx, conn, orderID, userID)
	payload, _ := json.Marshal(map[string]any{"order_id": orderID, "amount_cents": 1000})
	insertTicket(t, ctx, conn, ticketID, userID, string(payload), "pending", time.Now().Add(5*time.Minute))

	got, err := r.Confirm(ctx, ticketID, userID)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if got.Status != "confirmed" {
		t.Fatalf("want status=confirmed, got %q", got.Status)
	}
	if got.ConfirmedAt == nil {
		t.Fatal("want confirmed_at to be set")
	}

	// The order must have flipped to refunding.
	var orderStatus string
	if err := conn.QueryRowContext(ctx, `SELECT status FROM mock_orders WHERE order_id=?`, orderID).Scan(&orderStatus); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if orderStatus != "refunding" {
		t.Fatalf("want order status=refunding, got %q", orderStatus)
	}
}

func TestTicketsConfirmWrongUser(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	r := skill.NewTickets(conn)
	ctx := context.Background()

	insertPaidOrder(t, ctx, conn, "ORD-7002", "u-real-owner")
	payload, _ := json.Marshal(map[string]any{"order_id": "ORD-7002", "amount_cents": 500})
	insertTicket(t, ctx, conn, "t-imposter", "u-real-owner", string(payload), "pending", time.Now().Add(5*time.Minute))

	_, err := r.Confirm(ctx, "t-imposter", "u-attacker")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var ae *apperr.AppError
	if !errors.As(err, &ae) || ae.HTTPStatus != 403 {
		t.Fatalf("want 403, got %v", err)
	}
}

func TestTicketsConfirmDouble(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	r := skill.NewTickets(conn)
	ctx := context.Background()

	insertPaidOrder(t, ctx, conn, "ORD-7003", "u-7")
	payload, _ := json.Marshal(map[string]any{"order_id": "ORD-7003", "amount_cents": 100})
	insertTicket(t, ctx, conn, "t-double", "u-7", string(payload), "pending", time.Now().Add(5*time.Minute))

	if _, err := r.Confirm(ctx, "t-double", "u-7"); err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	_, err := r.Confirm(ctx, "t-double", "u-7")
	if err == nil {
		t.Fatal("second confirm should fail")
	}
	var ae *apperr.AppError
	if !errors.As(err, &ae) || ae.HTTPStatus != 409 {
		t.Fatalf("want 409, got %v", err)
	}
}

func TestTicketsConfirmExpiredTicket(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	r := skill.NewTickets(conn)
	ctx := context.Background()

	insertPaidOrder(t, ctx, conn, "ORD-7004", "u-7")
	payload, _ := json.Marshal(map[string]any{"order_id": "ORD-7004", "amount_cents": 100})
	// Insert with a past expiry so the Confirm path's row-level check fires.
	insertTicket(t, ctx, conn, "t-stale", "u-7", string(payload), "pending", time.Now().Add(-1*time.Minute))

	_, err := r.Confirm(ctx, "t-stale", "u-7")
	var ae *apperr.AppError
	if !errors.As(err, &ae) || ae.HTTPStatus != 409 {
		t.Fatalf("want 409, got %v", err)
	}
	// The store must also have flipped status to expired so future reads
	// reflect reality.
	var s string
	if err := conn.QueryRowContext(ctx, `SELECT status FROM skill_pending_tickets WHERE id='t-stale'`).Scan(&s); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if s != "expired" {
		t.Fatalf("want status=expired after Confirm, got %q", s)
	}
}

func TestTicketsCancelHappyPath(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	r := skill.NewTickets(conn)
	ctx := context.Background()

	insertPaidOrder(t, ctx, conn, "ORD-7005", "u-7")
	payload, _ := json.Marshal(map[string]any{"order_id": "ORD-7005", "amount_cents": 100})
	insertTicket(t, ctx, conn, "t-cancel", "u-7", string(payload), "pending", time.Now().Add(5*time.Minute))

	if err := r.Cancel(ctx, "t-cancel", "u-7"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	var s string
	if err := conn.QueryRowContext(ctx, `SELECT status FROM skill_pending_tickets WHERE id='t-cancel'`).Scan(&s); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if s != "cancelled" {
		t.Fatalf("want status=cancelled, got %q", s)
	}

	// Order status must NOT have flipped — Cancel is no-op on the order.
	var orderStatus string
	if err := conn.QueryRowContext(ctx, `SELECT status FROM mock_orders WHERE order_id='ORD-7005'`).Scan(&orderStatus); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if orderStatus != "paid" {
		t.Fatalf("Cancel must not mutate order; got %q", orderStatus)
	}
}

func TestTicketsCancelConfirmedIsNotFound(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	r := skill.NewTickets(conn)
	ctx := context.Background()

	insertPaidOrder(t, ctx, conn, "ORD-7006", "u-7")
	payload, _ := json.Marshal(map[string]any{"order_id": "ORD-7006", "amount_cents": 100})
	insertTicket(t, ctx, conn, "t-c", "u-7", string(payload), "pending", time.Now().Add(5*time.Minute))

	if _, err := r.Confirm(ctx, "t-c", "u-7"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := r.Cancel(ctx, "t-c", "u-7"); !errors.Is(err, skill.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestTicketsSweepExpired(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	r := skill.NewTickets(conn)
	ctx := context.Background()

	now := time.Now()
	payload, _ := json.Marshal(map[string]any{"order_id": "ORD-x", "amount_cents": 1})
	// 1 already-expired (should NOT be touched — SweepExpired only acts on status='pending')
	insertTicket(t, ctx, conn, "t-already", "u-1", string(payload), "expired", now.Add(-10*time.Minute))
	// 1 pending + past expiry → must flip to expired
	insertTicket(t, ctx, conn, "t-stale", "u-1", string(payload), "pending", now.Add(-1*time.Minute))
	// 1 pending + future expiry → must remain pending
	insertTicket(t, ctx, conn, "t-fresh", "u-1", string(payload), "pending", now.Add(5*time.Minute))
	// 1 confirmed + past expiry → must remain confirmed (only 'pending' is swept)
	insertTicket(t, ctx, conn, "t-confirmed", "u-1", string(payload), "confirmed", now.Add(-1*time.Minute))

	n, err := r.SweepExpired(ctx)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 swept, got %d", n)
	}

	// Second sweep must be a no-op.
	n2, _ := r.SweepExpired(ctx)
	if n2 != 0 {
		t.Fatalf("want 0 on second sweep, got %d", n2)
	}

	row := func(id string) string {
		var s string
		if err := conn.QueryRowContext(ctx, `SELECT status FROM skill_pending_tickets WHERE id=?`, id).Scan(&s); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		return s
	}
	if got := row("t-already"); got != "expired" {
		t.Errorf("t-already: want expired (unchanged), got %q", got)
	}
	if got := row("t-stale"); got != "expired" {
		t.Errorf("t-stale: want expired (just swept), got %q", got)
	}
	if got := row("t-fresh"); got != "pending" {
		t.Errorf("t-fresh: want pending (untouched), got %q", got)
	}
	if got := row("t-confirmed"); got != "confirmed" {
		t.Errorf("t-confirmed: want confirmed (untouched), got %q", got)
	}
}