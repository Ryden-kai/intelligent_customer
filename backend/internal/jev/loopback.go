// Package jev — loopback.go
//
// "效果回灌" per docs/v2-jev-loopback.md (ADR-008 picked the hybrid
// scheme: human labels + satisfaction feedback). v2.1 implements the
// capture half — recording the decision to jev_decisions so a later
// batch job can sample and label them.
//
// v2.3 will add the active learning half (close the loop by mutating
// template thresholds). For now this file just exposes:
//
//   - InMemoryLoopback : default no-op sink used by tests
//   - DBLoopback       : placeholder for the v2.3 active-learning path
//                        (no-op in v2.1; Orchestrator already persists)
//   - SatisfactionLoopback : bridge from /api/feedback to the loopback
//                        table — when a user rates the conversation,
//                        we mark the most recent Jev decision for the
//                        tenant as `accepted` (4-5) or `rejected`
//                        (1-3) with the rating as ground truth.

package jev

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
)

// InMemoryLoopback is the test / no-op sink. Keeps the last decision in
// memory and is concurrency-safe.
type InMemoryLoopback struct {
	mu        sync.Mutex
	decisions []Decision
	limit     int
}

// NewInMemoryLoopback returns a sink that keeps at most `limit` recent
// decisions (0 = unbounded). Useful for tests + the development demo.
func NewInMemoryLoopback(limit int) *InMemoryLoopback {
	if limit < 0 {
		limit = 0
	}
	return &InMemoryLoopback{limit: limit}
}

// Record appends d to the in-memory ring.
func (m *InMemoryLoopback) Record(ctx context.Context, d *Decision) error {
	if d == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.limit > 0 && len(m.decisions) >= m.limit {
		m.decisions = m.decisions[1:]
	}
	m.decisions = append(m.decisions, *d)
	return nil
}

// Snapshot returns a copy of the recorded decisions (newest last).
func (m *InMemoryLoopback) Snapshot() []Decision {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Decision, len(m.decisions))
	copy(out, m.decisions)
	return out
}

// DBLoopback is a placeholder for the v2.3 active-learning path. The
// Orchestrator already persists decisions directly, so v2.1
// DBLoopback.Record is a no-op kept for symmetry.
type DBLoopback struct {
	Repo *DecisionRepo
}

// NewDBLoopback returns a Loopback that defers to DecisionRepo. The
// repo can be nil; in that case Record is a silent no-op.
func NewDBLoopback(repo *DecisionRepo) *DBLoopback {
	return &DBLoopback{Repo: repo}
}

// Record is a no-op stub. The Orchestrator already writes the audit
// row; the loopback hook will later carry the active-learning signal
// that mutates templates (v2.3).
func (l *DBLoopback) Record(ctx context.Context, d *Decision) error {
	return nil
}

// SatisfactionLoopback bridges user ratings into the loopback table.
// Called by the Feedback service after the user submits a rating; it
// looks up the most recent Jev decision for the tenant and flips
// status to accepted/rejected accordingly.
//
// Connection between conversation and decision is loose (the decision
// row carries trace_id but no conversation_id in v2.1). v2.2 will add
// conversation_id; for now we mark the LATEST decision for the tenant
// as the conversation-level representative. This is intentionally
// approximate: we want feedback ingested even without a perfect join.
type SatisfactionLoopback struct {
	DB   *sql.DB
	Repo *DecisionRepo
}

// NewSatisfactionLoopback wires a SQL handle + DecisionRepo. Both can be
// nil; in that case Record is a silent no-op.
func NewSatisfactionLoopback(db *sql.DB, repo *DecisionRepo) *SatisfactionLoopback {
	return &SatisfactionLoopback{DB: db, Repo: repo}
}

// ErrNoDecisionForTenant is returned when we try to back-propagate a
// satisfaction rating but find no decision row to attach it to. Not a
// fatal error — callers should log and move on.
var ErrNoDecisionForTenant = errors.New("jev loopback: no decision found for tenant")

// ApplyRating records the (rating, comment) pair against the most recent
// decision for tenantID. status becomes "accepted" for ratings ≥ 4,
// "rejected" for ratings ≤ 3.
func (s *SatisfactionLoopback) ApplyRating(ctx context.Context, tenantID string, rating int, comment string) error {
	if s == nil || s.DB == nil || s.Repo == nil {
		return nil
	}
	if rating < 1 || rating > 5 {
		return errors.New("jev loopback: rating must be 1..5")
	}
	status := "rejected"
	if rating >= 4 {
		status = "accepted"
	}
	var id string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM jev_decisions
		 WHERE tenant_id=? AND status='decided'
		 ORDER BY created_at DESC LIMIT 1`, tenantID).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoDecisionForTenant
		}
		return err
	}
	ground := buildGroundTruth(rating, comment)
	return s.Repo.MarkReviewed(ctx, id, status, ground, "satisfaction_loopback")
}

// Record implements the Loopback interface but is a no-op for the
// satisfaction variant — ApplyRating is the entry point.
func (s *SatisfactionLoopback) Record(ctx context.Context, d *Decision) error { return nil }

func buildGroundTruth(rating int, comment string) string {
	out := struct {
		Rating  int    `json:"rating"`
		Comment string `json:"comment,omitempty"`
	}{rating, comment}
	b, _ := json.Marshal(out)
	return string(b)
}
