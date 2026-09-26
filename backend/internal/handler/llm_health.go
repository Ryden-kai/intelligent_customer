// Package handler — llm_health.go
//
// Admin endpoint that exposes the live state of the LLM router's
// circuit breakers so operators can spot a flapping provider without
// tailing logs. Wired in v2.1.1 (after Router 接 AgentChat).
//
//   GET /api/admin/llm/health
//     200 OK
//     {
//       "channels": [
//         {"slot":"primary","provider":"openai","model":"gpt-4o-mini","state":"closed"},
//         {"slot":"secondary","provider":"openrouter","model":"...","state":"open"},
//         ...
//       ]
//     }
//
// The endpoint is read-only and gated by the same JWT middleware as
// the rest of /admin.

package handler

import (
	"net/http"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/log"
)

// LLMHealth is a thin adapter over llm.Router — we only need the
// ChannelStates() snapshot, so we accept an interface instead of the
// concrete type to avoid an import cycle.
type LLMHealth struct {
	States func() []LLMChannelHealth // snapshot fn, lets tests inject deterministic state
	Logger zerolog.Logger
}

// LLMChannelHealth mirrors llm.ChannelHealth so we don't leak the llm
// package into the JSON contract. Tests and frontend both use this
// shape; if llm.ChannelHealth changes we'll need to update here.
type LLMChannelHealth struct {
	Slot     string `json:"slot"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	State    string `json:"state"`
}

// LLMHealthResponse is the JSON shape returned to operators.
type LLMHealthResponse struct {
	Channels []LLMChannelHealth `json:"channels"`
}

// ServeHTTP writes the breaker snapshot. Always 200 — callers compare
// against `state` values ("closed" / "open" / "half_open").
func (h *LLMHealth) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger)
	if h.States == nil {
		// No router wired — still 200 with an empty list so the admin
		// page renders cleanly instead of erroring.
		writeJSON(w, http.StatusOK, LLMHealthResponse{Channels: []LLMChannelHealth{}})
		return
	}
	src := h.States()
	out := make([]LLMChannelHealth, 0, len(src))
	for _, c := range src {
		out = append(out, LLMChannelHealth{
			Slot:     c.Slot,
			Provider: c.Provider,
			Model:    c.Model,
			State:    string(c.State),
		})
	}
	lg.Debug().Int("channels", len(out)).Msg("llm_health_snapshot")
	writeJSON(w, http.StatusOK, LLMHealthResponse{Channels: out})
}