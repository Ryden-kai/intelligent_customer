// DB-backed CRUD for the dynamic skill registry + audit/invocation +
// pending ticket lifecycle. Mock data the read-only skills depend on is
// also seeded here.

package skill

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/model"
)

var ErrNotFound = errors.New("not found")

// Skills is the DB CRUD layer for dynamic skills. Built-in skills never
// appear in the table; this repo only governs user-editable entries.
type Skills struct {
	DB *sql.DB
}

func NewSkills(db *sql.DB) *Skills { return &Skills{DB: db} }

// List returns all skills, enabled or not. Used by the admin endpoint.
func (r *Skills) List(ctx context.Context) ([]model.Skill, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT id, name, description, category, parameters_json, handler_kind, handler_config,
		        enabled, requires_human, read_only, created_at, updated_at
		 FROM skills ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Skill, 0)
	for rows.Next() {
		var s model.Skill
		var en, rh, ro int
		var createdAt, updatedAt int64
		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Category, &s.ParametersJSON,
			&s.HandlerKind, &s.HandlerConfig, &en, &rh, &ro, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		s.Enabled = en == 1
		s.RequiresHuman = rh == 1
		s.ReadOnly = ro == 1
		s.CreatedAt = time.UnixMilli(createdAt)
		s.UpdatedAt = time.UnixMilli(updatedAt)
		out = append(out, s)
	}
	return out, rows.Err()
}

// Create inserts a new skill row. Always stored as read_only=true,
// requires_human=false (registry layer also enforces this).
func (r *Skills) Create(ctx context.Context, in *model.Skill) error {
	if in.Name == "" || in.Description == "" {
		return apperr.BadRequest("name and description required")
	}
	if in.ParametersJSON == "" {
		in.ParametersJSON = `{"type":"object","properties":{}}`
	}
	if in.HandlerConfig == "" {
		in.HandlerConfig = "{}"
	}
	now := time.Now()
	in.ID = uuid.NewString()
	in.CreatedAt = now
	in.UpdatedAt = now
	in.ReadOnly = true
	in.RequiresHuman = false
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO skills(id, name, description, category, parameters_json, handler_kind, handler_config,
		                   enabled, requires_human, read_only, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,0,1,?,?)`,
		in.ID, in.Name, in.Description, in.Category, in.ParametersJSON, in.HandlerKind, in.HandlerConfig,
		boolToInt(in.Enabled), in.CreatedAt.UnixMilli(), in.UpdatedAt.UnixMilli())
	return err
}

// Update changes only description / parameters / handler config and the
// enabled flag. Name and category are immutable to keep audit rows sane.
func (r *Skills) Update(ctx context.Context, in *model.Skill) error {
	now := time.Now()
	in.UpdatedAt = now
	res, err := r.DB.ExecContext(ctx,
		`UPDATE skills SET description=?, category=?, parameters_json=?, handler_kind=?, handler_config=?,
		        enabled=?, updated_at=?
		 WHERE id=?`,
		in.Description, in.Category, in.ParametersJSON, in.HandlerKind, in.HandlerConfig,
		boolToInt(in.Enabled), in.UpdatedAt.UnixMilli(), in.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a skill by id. Returns ErrNotFound if id is unknown.
func (r *Skills) Delete(ctx context.Context, id string) error {
	res, err := r.DB.ExecContext(ctx, `DELETE FROM skills WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetEnabled toggles enabled flag without touching other fields. Used by
// the admin "启用/停用" buttons.
func (r *Skills) SetEnabled(ctx context.Context, id string, enabled bool) error {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE skills SET enabled=?, updated_at=? WHERE id=?`,
		boolToInt(enabled), time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ----------------------------------------------------------------------------
// Invocations (audit)
// ----------------------------------------------------------------------------

// Invocations is the audit log + persistent record of what the agent did.
type Invocations struct {
	DB *sql.DB
}

func NewInvocations(db *sql.DB) *Invocations { return &Invocations{DB: db} }

func (r *Invocations) Record(ctx context.Context, inv *model.SkillInvocation) error {
	if inv.ID == "" {
		inv.ID = uuid.NewString()
	}
	if inv.CreatedAt.IsZero() {
		inv.CreatedAt = time.Now()
	}
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO skill_invocations(id, conversation_id, skill_name, args_json, result_json, status,
		                                pending_ticket, trace_id, step_index, duration_ms, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		inv.ID, inv.ConversationID, inv.SkillName, inv.ArgsJSON, inv.ResultJSON, inv.Status,
		nullStr(inv.PendingTicket), inv.TraceID, inv.StepIndex, inv.DurationMS,
		inv.CreatedAt.UnixMilli())
	return err
}

// ListByConversation powers /api/admin/conversations/{id} enrichment.
func (r *Invocations) ListByConversation(ctx context.Context, convID string) ([]model.SkillInvocation, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT id, conversation_id, skill_name, args_json, result_json, status, pending_ticket,
		        trace_id, step_index, duration_ms, created_at
		 FROM skill_invocations WHERE conversation_id=? ORDER BY created_at ASC`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.SkillInvocation, 0)
	for rows.Next() {
		var (
			inv    model.SkillInvocation
			res    sql.NullString
			ticket sql.NullString
		)
		if err := rows.Scan(&inv.ID, &inv.ConversationID, &inv.SkillName, &inv.ArgsJSON,
			&res, &inv.Status, &ticket, &inv.TraceID, &inv.StepIndex, &inv.DurationMS, &inv.CreatedAt); err != nil {
			return nil, err
		}
		inv.ResultJSON = res.String
		inv.PendingTicket = ticket.String
		out = append(out, inv)
	}
	return out, rows.Err()
}

// ----------------------------------------------------------------------------
// Pending tickets (refund / human-in-the-loop)
// ----------------------------------------------------------------------------

type Tickets struct {
	DB *sql.DB
}

func NewTickets(db *sql.DB) *Tickets { return &Tickets{DB: db} }

// Get fetches a ticket by id.
func (r *Tickets) Get(ctx context.Context, id string) (*model.SkillPendingTicket, error) {
	row := r.DB.QueryRowContext(ctx,
		`SELECT id, conversation_id, user_id, skill_name, payload_json, summary, status, expires_at, created_at, confirmed_at
		 FROM skill_pending_tickets WHERE id=?`, id)
	var t model.SkillPendingTicket
	var expiresAt, createdAt int64
	var convID sql.NullString
	var confirmedAt sql.NullInt64
	if err := row.Scan(&t.ID, &convID, &t.UserID, &t.SkillName, &t.PayloadJSON,
		&t.Summary, &t.Status, &expiresAt, &createdAt, &confirmedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if convID.Valid {
		t.ConversationID = convID.String
	}
	t.ExpiresAt = time.UnixMilli(expiresAt)
	t.CreatedAt = time.UnixMilli(createdAt)
	if confirmedAt.Valid {
		ct := time.UnixMilli(confirmedAt.Int64)
		t.ConfirmedAt = &ct
	}
	return &t, nil
}

// Confirm atomically validates the ticket is still pending + not expired
// and applies the mutation (here: marks mock_orders status='refunding'
// and the ticket status='confirmed'). Returns the ticket with updated
// status.
func (r *Tickets) Confirm(ctx context.Context, ticketID, userID string) (*model.SkillPendingTicket, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // commit replaces

	// Lock the row so two confirm clicks can't double-fire.
	var (
		ownerID, skillName, payload string
		curStatus                  string
		expiresAt                  int64
	)
	err = tx.QueryRowContext(ctx,
		`SELECT user_id, skill_name, payload_json, status, expires_at FROM skill_pending_tickets WHERE id=?`,
		ticketID).Scan(&ownerID, &skillName, &payload, &curStatus, &expiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if ownerID != userID {
		return nil, apperr.Forbidden("ticket belongs to a different user")
	}
	if curStatus != "pending" {
		return nil, apperr.Conflict("ticket already " + curStatus)
	}
	if expiresAt < time.Now().UnixMilli() {
		_, _ = tx.ExecContext(ctx, `UPDATE skill_pending_tickets SET status='expired' WHERE id=?`, ticketID)
		_ = tx.Commit()
		return nil, apperr.Conflict("ticket expired")
	}

	// Apply the mutation. The set of supported mutations is hard-coded
	// because dynamic skills cannot carry mutations (registry enforces).
	switch skillName {
	case "apply_refund":
		var p struct {
			OrderID     string `json:"order_id"`
			AmountCents int64  `json:"amount_cents"`
		}
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			return nil, apperr.BadRequest("ticket payload corrupt").WithCause(err)
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE mock_orders SET status='refunding' WHERE order_id=? AND user_id=? AND status NOT IN ('refunded','refunding')`,
			p.OrderID, ownerID)
		if err != nil {
			return nil, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return nil, apperr.Conflict("order not eligible for refund (status changed)")
		}
	default:
		return nil, apperr.BadRequest("unknown skill on ticket: " + skillName)
	}

	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx,
		`UPDATE skill_pending_tickets SET status='confirmed', confirmed_at=? WHERE id=?`, now, ticketID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.Get(ctx, ticketID)
}

// Cancel marks a pending ticket cancelled. The user can call this from
// the frontend "不再需要" button.
func (r *Tickets) Cancel(ctx context.Context, ticketID, userID string) error {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE skill_pending_tickets SET status='cancelled' WHERE id=? AND user_id=? AND status='pending'`,
		ticketID, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SweepExpired marks any pending ticket past expiry as expired. Called by
// the agent before each Confirm attempt; cheap because the index covers
// (status, expires_at).
func (r *Tickets) SweepExpired(ctx context.Context) (int, error) {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE skill_pending_tickets SET status='expired'
		 WHERE status='pending' AND expires_at < ?`, time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ----------------------------------------------------------------------------
// Mock data seeding
// ----------------------------------------------------------------------------

// SeedMockData fills mock_orders / mock_coupons / mock_points with a
// fixed demo set so the read-only skills always have something to
// return. Idempotent (uses ON CONFLICT DO NOTHING).
func SeedMockData(ctx context.Context, db *sql.DB, demoUserID string) error {
	if demoUserID == "" {
		demoUserID = "demo-user"
	}
	now := time.Now()
	type orderRow struct {
		ID, Status, UserID, Carrier, Tracking string
		Amount                                int64
		PaidAt, ShippedAt, DeliveredAt        int64
	}
	orders := []orderRow{
		{"ORD-1001", "shipped", demoUserID, "顺丰", "SF1234567890", 19900, now.Add(-72 * time.Hour).UnixMilli(), now.Add(-48 * time.Hour).UnixMilli(), 0},
		{"ORD-1002", "delivered", demoUserID, "中通", "ZT9876543210", 8800, now.Add(-240 * time.Hour).UnixMilli(), now.Add(-216 * time.Hour).UnixMilli(), now.Add(-168 * time.Hour).UnixMilli()},
		{"ORD-1003", "pending", demoUserID, "", "", 12000, 0, 0, 0},
		{"ORD-2001", "shipped", "u-other", "京东", "JD111222333", 29900, now.Add(-12 * time.Hour).UnixMilli(), now.Add(-6 * time.Hour).UnixMilli(), 0},
	}
	for _, o := range orders {
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO mock_orders(order_id, user_id, status, amount_cents, carrier, tracking_no, paid_at, shipped_at, delivered_at, created_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?)`,
			o.ID, o.UserID, o.Status, o.Amount, o.Carrier, o.Tracking,
			nullInt(o.PaidAt), nullInt(o.ShippedAt), nullInt(o.DeliveredAt), now.UnixMilli()); err != nil {
			return fmt.Errorf("seed mock_orders %s: %w", o.ID, err)
		}
	}

	coupons := []struct {
		Code       string
		Amount     int64
		Min        int64
		ExpiresIn  time.Duration
	}{
		{"WELCOME10", 1000, 5000, 30 * 24 * time.Hour},
		{"VIP50", 5000, 50000, 60 * 24 * time.Hour},
		{"EXPIRED", 2000, 0, -1 * time.Hour},
	}
	for _, c := range coupons {
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO mock_coupons(id, user_id, code, amount_cents, min_order_cents, expires_at, used)
			 VALUES (?,?,?,?,?,?,0)`,
			uuid.NewString(), demoUserID, c.Code, c.Amount, c.Min, now.Add(c.ExpiresIn).UnixMilli()); err != nil {
			return fmt.Errorf("seed mock_coupons %s: %w", c.Code, err)
		}
	}

	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO mock_points(user_id, balance, updated_at) VALUES (?,?,?)`,
		demoUserID, int64(2480), now.UnixMilli()); err != nil {
		return fmt.Errorf("seed mock_points: %w", err)
	}
	return nil
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(i int64) any {
	if i == 0 {
		return nil
	}
	return i
}