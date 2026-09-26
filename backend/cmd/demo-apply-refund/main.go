// demo-apply-refund is a CLI that calls the apply_refund skill directly,
// bypassing the LLM/agent layer. Used by scripts/demo.sh to demonstrate
// the refund happy path without needing a real LLM key.
//
// Usage:
//   demo-apply-refund --db <path> --user <id> --order <order_id> \
//                     --amount <cents> --reason <text>
//
// Prints the resulting ticket id as JSON on stdout:
//   {"ticketId":"<uuid>","summary":"..."}
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"intelligent_customer/backend/internal/skill"
)

func main() {
	dbPath := flag.String("db", "", "SQLite database path")
	userID := flag.String("user", "", "user id (must match the order's owner)")
	orderID := flag.String("order", "", "order id")
	amountCents := flag.Int64("amount", 0, "refund amount in cents")
	reason := flag.String("reason", "demo refund", "refund reason")
	flag.Parse()

	if *dbPath == "" || *userID == "" || *orderID == "" || *amountCents <= 0 {
		fmt.Fprintln(os.Stderr, "all of --db, --user, --order, --amount>0 are required")
		os.Exit(2)
	}

	// Open DB through skill.MockData + ticket TTL. The skill package
	// doesn't expose the raw *sql.DB constructor — we go through
	// NewTickets to construct the same shape the registry uses.
	db, err := openSQLite(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	mock := &skill.MockData{DB: db, TicketTTL: 5 * 60 * 1_000_000_000} // 5 min
	reg := skill.NewRegistry()
	if err := skill.RegisterBuiltins(reg, mock); err != nil {
		fmt.Fprintf(os.Stderr, "register builtins: %v\n", err)
		os.Exit(1)
	}

	def, err := reg.Get("apply_refund")
	if err != nil {
		fmt.Fprintf(os.Stderr, "lookup apply_refund: %v\n", err)
		os.Exit(1)
	}

	argsJSON := buildArgsJSON(*orderID, *reason, *amountCents, *userID)
	res, err := def.Execute(context.Background(), argsJSON)
	if err != nil {
		fmt.Fprintf(os.Stderr, "apply_refund failed: %v\n", err)
		os.Exit(1)
	}

	out := map[string]any{
		"ticketId": res.PendingTicket,
		"summary":  res.Summary,
		"status":   res.Status,
	}
	if data, ok := res.Data["amount_cents"]; ok {
		out["amount_cents"] = data
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
	if res.Status != skill.StatusPendingHuman {
		fmt.Fprintf(os.Stderr, "warning: status=%s (expected pending_human)\n", res.Status)
	}
}

// buildArgsJSON serialises the apply_refund args in the exact shape the
// skill expects. Pulled out so unit tests don't need a real *sql.DB.
func buildArgsJSON(orderID, reason string, amountCents int64, userID string) string {
	return fmt.Sprintf(`{"order_id":%q,"reason":%q,"amount_cents":%d,"user_id":%q}`,
		orderID, reason, amountCents, userID)
}