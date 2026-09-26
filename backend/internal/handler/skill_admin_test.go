package handler_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/handler"
	"intelligent_customer/backend/internal/skill"
	"intelligent_customer/backend/internal/testutil"
)

// helper local to this package: insert a paid mock_order + a pending
// apply_refund ticket. conversation_id left NULL matches the production
// flow (the agent back-fills it after the fact).
func seedRefundTicket(t *testing.T, ctx context.Context, db *sql.DB, orderID, ticketID, userID string) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`INSERT INTO mock_orders(order_id, user_id, status, amount_cents, paid_at, created_at)
		 VALUES(?, ?, 'paid', 19900, ?, ?)`,
		orderID, userID, time.Now().UnixMilli(), time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO skill_pending_tickets(id, conversation_id, user_id, skill_name, payload_json, summary, status, expires_at, created_at)
		 VALUES(?, NULL, ?, 'apply_refund', ?, 'apply refund ticket', 'pending', ?, ?)`,
		ticketID, userID,
		`{"order_id":"`+orderID+`","amount_cents":1000}`,
		time.Now().Add(5*time.Minute).UnixMilli(), time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("insert ticket: %v", err)
	}
}

func newRefundConfirmHarness(t *testing.T) (*sql.DB, *handler.RefundConfirm, func()) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	hc := &handler.RefundConfirm{
		Tickets: skill.NewTickets(conn),
		Logger:  zerolog.Nop(),
	}
	return conn, hc, cleanup
}

func readStatus(t *testing.T, ctx context.Context, db *sql.DB, table, idCol, idVal string) string {
	t.Helper()
	var s string
	if err := db.QueryRowContext(ctx, `SELECT status FROM `+table+` WHERE `+idCol+`=?`, idVal).Scan(&s); err != nil {
		t.Fatalf("read status %s.%s=%s: %v", table, idCol, idVal, err)
	}
	return s
}

func TestRefundConfirmHappyPath(t *testing.T) {
	conn, hc, cleanup := newRefundConfirmHarness(t)
	defer cleanup()
	ctx := context.Background()
	seedRefundTicket(t, ctx, conn, "ORD-9001", "t-hc-happy", "u-hc")

	body, _ := json.Marshal(map[string]string{"ticketId": "t-hc-happy", "userId": "u-hc"})
	req := httptest.NewRequest(http.MethodPost, "/api/skills/confirm", bytes.NewReader(body))
	w := httptest.NewRecorder()
	hc.Confirm(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}

	if got := readStatus(t, ctx, conn, "skill_pending_tickets", "id", "t-hc-happy"); got != "confirmed" {
		t.Fatalf("ticket status: want confirmed, got %q", got)
	}
	if got := readStatus(t, ctx, conn, "mock_orders", "order_id", "ORD-9001"); got != "refunding" {
		t.Fatalf("order status: want refunding, got %q", got)
	}
}

func TestRefundConfirmWrongUser(t *testing.T) {
	conn, hc, cleanup := newRefundConfirmHarness(t)
	defer cleanup()
	ctx := context.Background()
	seedRefundTicket(t, ctx, conn, "ORD-9002", "t-hc-imposter", "u-real")

	body, _ := json.Marshal(map[string]string{"ticketId": "t-hc-imposter", "userId": "u-attacker"})
	req := httptest.NewRequest(http.MethodPost, "/api/skills/confirm", bytes.NewReader(body))
	w := httptest.NewRecorder()
	hc.Confirm(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", w.Code, w.Body.String())
	}
	// The ticket must remain pending.
	if got := readStatus(t, ctx, conn, "skill_pending_tickets", "id", "t-hc-imposter"); got != "pending" {
		t.Fatalf("ticket status: want pending, got %q", got)
	}
}

func TestRefundConfirmMissingFields(t *testing.T) {
	conn, hc, cleanup := newRefundConfirmHarness(t)
	defer cleanup()
	ctx := context.Background()
	seedRefundTicket(t, ctx, conn, "ORD-9003", "t-hc-missing", "u-hc")

	body, _ := json.Marshal(map[string]string{"ticketId": "t-hc-missing"}) // userId missing
	req := httptest.NewRequest(http.MethodPost, "/api/skills/confirm", bytes.NewReader(body))
	w := httptest.NewRecorder()
	hc.Confirm(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRefundConfirmUnknownTicket(t *testing.T) {
	_, hc, cleanup := newRefundConfirmHarness(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{"ticketId": "does-not-exist", "userId": "u-x"})
	req := httptest.NewRequest(http.MethodPost, "/api/skills/confirm", bytes.NewReader(body))
	w := httptest.NewRecorder()
	hc.Confirm(w, req)
	if w.Code != http.StatusForbidden {
		// Tickets.Confirm returns Forbidden when user_id mismatches OR
		// the row is missing — both expose the same "this isn't yours"
		// response so we can't distinguish. That's intentional: the 403
		// avoids a user-existence oracle.
		t.Fatalf("want 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRefundConfirmBadJSON(t *testing.T) {
	_, hc, cleanup := newRefundConfirmHarness(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/skills/confirm", bytes.NewReader([]byte("{not json")))
	w := httptest.NewRecorder()
	hc.Confirm(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestRefundCancelHappyPath(t *testing.T) {
	conn, hc, cleanup := newRefundConfirmHarness(t)
	defer cleanup()
	ctx := context.Background()
	seedRefundTicket(t, ctx, conn, "ORD-9005", "t-hc-cancel", "u-hc")

	body, _ := json.Marshal(map[string]string{"ticketId": "t-hc-cancel", "userId": "u-hc"})
	req := httptest.NewRequest(http.MethodPost, "/api/skills/cancel", bytes.NewReader(body))
	w := httptest.NewRecorder()
	hc.Cancel(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d body=%s", w.Code, w.Body.String())
	}
	if got := readStatus(t, ctx, conn, "skill_pending_tickets", "id", "t-hc-cancel"); got != "cancelled" {
		t.Fatalf("ticket status: want cancelled, got %q", got)
	}
	// Order status must NOT change on cancel.
	if got := readStatus(t, ctx, conn, "mock_orders", "order_id", "ORD-9005"); got != "paid" {
		t.Fatalf("order status after cancel: want paid, got %q", got)
	}
}

func TestRefundCancelAlreadyConfirmed(t *testing.T) {
	conn, hc, cleanup := newRefundConfirmHarness(t)
	defer cleanup()
	ctx := context.Background()
	seedRefundTicket(t, ctx, conn, "ORD-9006", "t-hc-already", "u-hc")

	// First confirm.
	body, _ := json.Marshal(map[string]string{"ticketId": "t-hc-already", "userId": "u-hc"})
	req := httptest.NewRequest(http.MethodPost, "/api/skills/confirm", bytes.NewReader(body))
	w := httptest.NewRecorder()
	hc.Confirm(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first confirm: want 200, got %d", w.Code)
	}

	// Now cancel — must 404 because the row no longer matches status=pending.
	body, _ = json.Marshal(map[string]string{"ticketId": "t-hc-already", "userId": "u-hc"})
	req = httptest.NewRequest(http.MethodPost, "/api/skills/cancel", bytes.NewReader(body))
	w = httptest.NewRecorder()
	hc.Cancel(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 on cancel-after-confirm, got %d body=%s", w.Code, w.Body.String())
	}

	// And the order still flipped (confirm side-effect persisted).
	if got := readStatus(t, ctx, conn, "mock_orders", "order_id", "ORD-9006"); got != "refunding" {
		t.Fatalf("order after cancel-of-confirmed: want refunding, got %q", got)
	}
}

// Sanity: the body is captured by httptest.ResponseRecorder.Body;
// no manual io reads needed.
var _ = httptest.NewRecorder