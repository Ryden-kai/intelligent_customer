// Package sse — handler.go
//
// Implements GET /api/admin/stream (SSE endpoint for the admin realtime
// refresh feature). The handler:
//   - resolves tenantID from context (TenantGuard has already done this);
//   - registers a Subscriber on the broker;
//   - streams "data: <json>\n\n" frames for every event;
//   - sends an initial "stats_update" snapshot so the client doesn't
//     have to wait for the first tick;
//   - returns on context cancellation (client disconnect).
//
// Notes:
//   - We do NOT add a server-side heartbeat (PRD Q-C).
//   - We use Flush() after every Write so the proxy buffers don't
//     coalesce multiple frames.
package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/tenant"
)

// Handler holds the broker + the dependencies needed to build initial
// snapshots on connect (Stats + Decisions list).
type Handler struct {
	Broker       *Broker
	DecisionRepo *jev.DecisionRepo
	Logger       zerolog.Logger
	// SnapshotInterval is the cadence at which we proactively push a
	// stats_update from inside the handler (in addition to broker-driven
	// Publish calls from elsewhere). Default = 5s.
	SnapshotInterval time.Duration
}

// ServeHTTP streams SSE frames for the request's tenant.
//
// Auth / RBAC / TenantGuard are all expected to be mounted by the
// router before this handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "sse.stream").Logger()
	tenantID := tenant.FromContext(r.Context()).ID

	// SSE headers — must be set before any Write.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // tell nginx not to buffer
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		lg.Error().Msg("sse_no_flusher")
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	sub := h.Broker.Subscribe(tenantID)
	defer h.Broker.Unsubscribe(tenantID, sub)
	lg.Debug().Str("sub_id", sub.ID()).Str("tenant", tenantID).Msg("sse_subscribe")

	// Initial snapshot so the client has data on frame 0.
	if snap := h.snapshot(r.Context(), tenantID); snap != nil {
		writeFrame(w, flusher, snap)
	}

	interval := h.SnapshotInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			lg.Debug().Str("sub_id", sub.ID()).Msg("sse_disconnect")
			return
		case <-ticker.C:
			if snap := h.snapshot(r.Context(), tenantID); snap != nil {
				writeFrame(w, flusher, snap)
			}
		case ev, open := <-sub.Events():
			if !open {
				lg.Debug().Str("sub_id", sub.ID()).Msg("sse_subscriber_closed")
				return
			}
			writeFrame(w, flusher, &ev)
		}
	}
}

// snapshot computes the current Stats payload for tenantID and
// returns it as an SSE Event. Returns nil if Stats can't be computed
// (e.g. tenant_id missing); the handler logs the error and just
// sends nothing for this tick.
func (h *Handler) snapshot(ctx context.Context, tenantID string) *Event {
	if h.DecisionRepo == nil {
		return nil
	}
	stats, err := h.DecisionRepo.Stats(ctx, tenantID, 0)
	if err != nil {
		// Don't spam logs — Stats can fail during early boot when
		// the jev_decisions table is empty; that's expected.
		return nil
	}
	acceptRate := computeAcceptRate(stats)
	frame := StatsSnapshot{
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		TotalDecisions: stats.Total,
		FallbackRate:   stats.FallbackRate,
		P95LatencyMS:   stats.P95LatencyMS,
		AcceptRate:     acceptRate,
		ByTemplate:     snapshotByTemplate(stats.ByTemplate),
	}
	buf, err := json.Marshal(frame)
	if err != nil {
		return nil
	}
	return &Event{Type: "stats_update", Payload: string(buf)}
}

// StatsSnapshot is the JSON shape of the SSE payload for "stats_update".
//
// It mirrors PRD §6.7 + adds byTemplate for the recharts BarChart.
type StatsSnapshot struct {
	Timestamp      string             `json:"timestamp"`
	TotalDecisions int                `json:"total_decisions"`
	FallbackRate   float64            `json:"fallback_rate"`
	P95LatencyMS   int                `json:"p95_ms"`
	AcceptRate     float64            `json:"accept_rate"`
	ByTemplate     []TemplateSnapshot `json:"by_template,omitempty"`
}

// TemplateSnapshot is the per-template row in the StatsSnapshot.
type TemplateSnapshot struct {
	Template string `json:"template"`
	Count    int    `json:"count"`
	Fallback int    `json:"fallback"`
}

// snapshotByTemplate converts jev.TemplateStat rows into TemplateSnapshot.
func snapshotByTemplate(rows []jev.TemplateStat) []TemplateSnapshot {
	if len(rows) == 0 {
		return nil
	}
	out := make([]TemplateSnapshot, 0, len(rows))
	for _, r := range rows {
		out = append(out, TemplateSnapshot{
			Template: r.Template,
			Count:    r.Count,
			Fallback: r.Fallback,
		})
	}
	return out
}

// computeAcceptRate approximates accept rate from the stats payload.
//
// jev.DecisionRepo.Stats does not expose an "accepted vs rejected"
// field directly; we approximate accept rate as 1 - fallbackRate
// (every decision that wasn't a fallback landed on a Jev output).
//
// v2.2.1+ may replace this with a real accept / reject aggregate
// from jev_decisions.status.
func computeAcceptRate(s *jev.Stats) float64 {
	if s == nil || s.Total == 0 {
		return 0
	}
	rate := 1 - s.FallbackRate
	if rate < 0 {
		rate = 0
	}
	return rate
}

// writeFrame writes one SSE frame and flushes.
//
// Format per HTML5 spec:
//   "event: <type>\n" (optional)
//   "data: <payload>\n"
//   "\n"
//
// We omit "event:" so EventSource consumers always use onmessage.
// JSON strings are safe to embed verbatim (no newlines in payloads).
func writeFrame(w http.ResponseWriter, f http.Flusher, ev *Event) {
	if ev == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Payload)
	f.Flush()
}