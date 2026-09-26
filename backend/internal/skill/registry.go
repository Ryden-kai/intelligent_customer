// Built-in skills. Hard-coded Go functions for the three operations we
// want first-class: order/coupon query (read-only, mock data) and refund
// (mutating, human-in-the-loop).
//
// The agent decides when to call these; the LLM just gets a tool-list
// snapshot and is expected to follow the schema. Every Execute function
// returns an ExecutionResult whose Status field encodes the success path.

package skill

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DB is the slice of *sql.DB the built-in skills need. Concrete repo
// wrappers live in internal/skill/store.go; the registry just receives
// the bare handle to keep coupling low.
type DB = sql.DB

// MockData holds the read-only backends for query_* skills. The values
// here are seeded by store.go's SeedMockData and updated when admin
// mutations happen (none today).
type MockData struct {
	DB        *sql.DB
	TicketTTL time.Duration
}

// RegisterBuiltins wires the three skills into the registry. Order matters
// when name collisions happen — but we explicitly fail on collision, so
// it's just a fixed list.
func RegisterBuiltins(r *Registry, mock *MockData) error {
	if err := r.Register(Definition{
		Name:           "query_order",
		Description:    "查询订单状态、物流信息。输入 order_id 返回订单状态、物流公司、运单号。",
		Category:       "order",
		ParametersJSON: `{"type":"object","properties":{"order_id":{"type":"string","description":"订单号"}},"required":["order_id"]}`,
		Enabled:        true,
		RequiresHuman:  false,
		ReadOnly:       true,
		Execute:        queryOrderExecutor(mock),
	}); err != nil {
		return err
	}
	if err := r.Register(Definition{
		Name:           "query_coupon",
		Description:    "查询当前用户的可用优惠券和积分余额。无参数。",
		Category:       "coupon",
		ParametersJSON: `{"type":"object","properties":{},"required":[]}`,
		Enabled:        true,
		RequiresHuman:  false,
		ReadOnly:       true,
		Execute:        queryCouponExecutor(mock),
	}); err != nil {
		return err
	}
	if err := r.Register(Definition{
		Name:           "apply_refund",
		Description:    "为指定订单创建退款申请。输入 order_id、reason、amount_cents。此操作会生成待人工确认工单，客服在二次确认前不会实际退款。",
		Category:       "refund",
		ParametersJSON: `{"type":"object","properties":{"order_id":{"type":"string"},"reason":{"type":"string"},"amount_cents":{"type":"integer","minimum":1}},"required":["order_id","reason","amount_cents"]}`,
		Enabled:        true,
		RequiresHuman:  true,
		ReadOnly:       false,
		Execute:        applyRefundExecutor(mock),
	}); err != nil {
		return err
	}
	return nil
}

// ----------------------------------------------------------------------------
// query_order
// ----------------------------------------------------------------------------

func queryOrderExecutor(mock *MockData) func(ctx context.Context, argsJSON string) (ExecutionResult, error) {
	return func(ctx context.Context, argsJSON string) (ExecutionResult, error) {
		var args struct {
			OrderID string `json:"order_id"`
			UserID  string `json:"user_id"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return ExecutionResult{}, fmt.Errorf("query_order: invalid args: %w", err)
		}
		if strings.TrimSpace(args.OrderID) == "" {
			return ExecutionResult{}, errors.New("query_order: order_id required")
		}
		if mock == nil || mock.DB == nil {
			return ExecutionResult{Status: StatusOK, Data: map[string]any{
				"order_id":  args.OrderID,
				"status":    "unknown",
				"message":   "mock data not configured",
			}, Summary: "mock backend not wired"}, nil
		}

		var (
			status       string
			amount       int64
			carrier      string
			tracking     string
			paidAt       sql.NullInt64
			shippedAt    sql.NullInt64
			deliveredAt  sql.NullInt64
			ownerID      string
		)
		err := mock.DB.QueryRowContext(ctx,
			`SELECT user_id, status, amount_cents, carrier, tracking_no, paid_at, shipped_at, delivered_at
			 FROM mock_orders WHERE order_id = ?`, args.OrderID).
			Scan(&ownerID, &status, &amount, &carrier, &tracking, &paidAt, &shippedAt, &deliveredAt)
		if err == sql.ErrNoRows {
			return ExecutionResult{Status: StatusOK, Data: map[string]any{
				"order_id": args.OrderID,
				"status":   "not_found",
			}, Summary: "订单不存在"}, nil
		}
		if err != nil {
			return ExecutionResult{}, fmt.Errorf("query_order: %w", err)
		}

		data := map[string]any{
			"order_id":     args.OrderID,
			"status":       status,
			"amount_cents": amount,
			"carrier":      carrier,
			"tracking_no":  tracking,
		}
		if paidAt.Valid {
			data["paid_at"] = time.UnixMilli(paidAt.Int64).UTC().Format(time.RFC3339)
		}
		if shippedAt.Valid {
			data["shipped_at"] = time.UnixMilli(shippedAt.Int64).UTC().Format(time.RFC3339)
		}
		if deliveredAt.Valid {
			data["delivered_at"] = time.UnixMilli(deliveredAt.Int64).UTC().Format(time.RFC3339)
		}
		// If a UserID was passed and doesn't match, redact the amount.
		// This is the kind of thing a skill MUST think about — PII
		// leakage via tool results.
		if args.UserID != "" && ownerID != "" && args.UserID != ownerID {
			return ExecutionResult{Status: StatusOK, Data: map[string]any{
				"order_id": args.OrderID,
				"status":   "permission_denied",
				"message":  "该订单不属于当前用户",
			}, Summary: "权限拒绝"}, nil
		}
		return ExecutionResult{Status: StatusOK, Data: data, Summary: fmt.Sprintf("订单 %s 当前状态 %s", args.OrderID, status)}, nil
	}
}

// ----------------------------------------------------------------------------
// query_coupon
// ----------------------------------------------------------------------------

func queryCouponExecutor(mock *MockData) func(ctx context.Context, argsJSON string) (ExecutionResult, error) {
	return func(ctx context.Context, argsJSON string) (ExecutionResult, error) {
		var args struct {
			UserID string `json:"user_id"`
		}
		_ = json.Unmarshal([]byte(argsJSON), &args)
		if strings.TrimSpace(args.UserID) == "" {
			return ExecutionResult{}, errors.New("query_coupon: user_id required")
		}
		if mock == nil || mock.DB == nil {
			return ExecutionResult{Status: StatusOK, Data: map[string]any{
				"coupons": []any{},
				"points":  0,
			}, Summary: "mock backend not wired"}, nil
		}

		rows, err := mock.DB.QueryContext(ctx,
			`SELECT code, amount_cents, min_order_cents, expires_at FROM mock_coupons
			 WHERE user_id = ? AND used = 0 AND expires_at > ? ORDER BY expires_at ASC`,
			args.UserID, time.Now().UnixMilli())
		if err != nil {
			return ExecutionResult{}, fmt.Errorf("query_coupon: %w", err)
		}
		defer rows.Close()
		coupons := make([]map[string]any, 0, 4)
		for rows.Next() {
			var (
				code        string
				amount      int64
				minOrder    int64
				expiresAt   int64
			)
			if err := rows.Scan(&code, &amount, &minOrder, &expiresAt); err != nil {
				return ExecutionResult{}, fmt.Errorf("query_coupon scan: %w", err)
			}
			coupons = append(coupons, map[string]any{
				"code":            code,
				"amount_cents":    amount,
				"min_order_cents": minOrder,
				"expires_at":      time.UnixMilli(expiresAt).UTC().Format("2006-01-02"),
			})
		}
		if err := rows.Err(); err != nil {
			return ExecutionResult{}, fmt.Errorf("query_coupon rows: %w", err)
		}

		var points int64
		_ = mock.DB.QueryRowContext(ctx, `SELECT balance FROM mock_points WHERE user_id = ?`, args.UserID).Scan(&points)

		return ExecutionResult{Status: StatusOK, Data: map[string]any{
			"coupons": coupons,
			"points":  points,
		}, Summary: fmt.Sprintf("%d 张可用券 + %d 积分", len(coupons), points)}, nil
	}
}

// ----------------------------------------------------------------------------
// apply_refund (pending human)
// ----------------------------------------------------------------------------

func applyRefundExecutor(mock *MockData) func(ctx context.Context, argsJSON string) (ExecutionResult, error) {
	return func(ctx context.Context, argsJSON string) (ExecutionResult, error) {
		var args struct {
			OrderID     string `json:"order_id"`
			Reason      string `json:"reason"`
			AmountCents int64  `json:"amount_cents"`
			UserID      string `json:"user_id"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return ExecutionResult{}, fmt.Errorf("apply_refund: invalid args: %w", err)
		}
		if args.OrderID == "" || args.Reason == "" || args.AmountCents <= 0 {
			return ExecutionResult{}, errors.New("apply_refund: order_id, reason, amount_cents>0 required")
		}
		if args.UserID == "" {
			return ExecutionResult{}, errors.New("apply_refund: user_id required (must be passed by agent)")
		}
		if mock == nil || mock.DB == nil {
			return ExecutionResult{}, errors.New("apply_refund: mock backend not wired")
		}

		// Hard validation up front: order exists, belongs to user, not already refunded.
		var (
			ownerID    string
			curStatus  string
			curAmount  int64
		)
		err := mock.DB.QueryRowContext(ctx,
			`SELECT user_id, status, amount_cents FROM mock_orders WHERE order_id = ?`, args.OrderID).
			Scan(&ownerID, &curStatus, &curAmount)
		if err == sql.ErrNoRows {
			return ExecutionResult{}, errors.New("apply_refund: order not found")
		}
		if err != nil {
			return ExecutionResult{}, fmt.Errorf("apply_refund: lookup: %w", err)
		}
		if ownerID != args.UserID {
			return ExecutionResult{}, errors.New("apply_refund: order does not belong to user")
		}
		if curStatus == "refunding" || curStatus == "refunded" {
			return ExecutionResult{}, errors.New("apply_refund: order already in refund flow")
		}
		if args.AmountCents > curAmount {
			return ExecutionResult{Status: StatusOK, Data: map[string]any{
				"order_id": args.OrderID,
				"status":   "rejected",
				"message":  fmt.Sprintf("退款金额 %d 分超过订单金额 %d 分", args.AmountCents, curAmount),
			}, Summary: "退款金额超限"}, nil
		}

		ttl := mock.TicketTTL
		if ttl <= 0 {
			ttl = 5 * time.Minute
		}
		now := time.Now()
		ticket := uuid.NewString()
		summary := fmt.Sprintf("订单 %s 退款 %d.%02d 元，原因：%s",
			args.OrderID, args.AmountCents/100, args.AmountCents%100, truncate(args.Reason, 64))

		payload, _ := json.Marshal(map[string]any{
			"order_id":     args.OrderID,
			"reason":       args.Reason,
			"amount_cents": args.AmountCents,
		})

		_, err = mock.DB.ExecContext(ctx,
			`INSERT INTO skill_pending_tickets(id, conversation_id, user_id, skill_name, payload_json, summary, status, expires_at, created_at)
			 VALUES (?, NULL, ?, 'apply_refund', ?, ?, 'pending', ?, ?)`,
			ticket, args.UserID, string(payload), summary, now.Add(ttl).UnixMilli(), now.UnixMilli())
		if err != nil {
			return ExecutionResult{}, fmt.Errorf("apply_refund: insert ticket: %w", err)
		}

		return ExecutionResult{
			Status:        StatusPendingHuman,
			PendingTicket: ticket,
			Data: map[string]any{
				"order_id":     args.OrderID,
				"amount_cents": args.AmountCents,
				"reason":       args.Reason,
				"summary":      summary,
				"expires_in_s": int(ttl.Seconds()),
			},
			Summary: "已生成待确认退款工单 " + ticket,
		}, nil
	}
}

func truncate(s string, n int) string {
	if len([]byte(s)) <= n {
		return s
	}
	return string([]byte(s)[:n]) + "…"
}