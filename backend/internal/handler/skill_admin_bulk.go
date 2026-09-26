// Package handler — skill_admin_bulk.go
//
// v2.2 PR5: bulk enable/disable for skills. Kept in a separate file so
// the PR1-2 skill_admin.go stays focused.
//
//   POST /api/admin/skills/toggle-batch   skills.toggle
//
// Body: { ids: string[], enabled: bool }
// Response: { total, succeeded, results: [{id, status, error?}] }
//
// Idempotent: toggling a skill that already has the desired state counts
// as success (no audit emitted in that case so we don't flood the log
// when an operator clicks twice).

package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/audit"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/model"
	"intelligent_customer/backend/internal/skill"
)

// ToggleBatch enables or disables a list of skills in one shot.
//
// The admin UI sends ids from the table checkbox column; on success each
// row's enabled flag is updated in the DB and the in-memory registry is
// reloaded once at the end (cheaper than reloading per row).
func (h *SkillAdmin) ToggleBatch(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "skill_admin.toggle_batch").Logger()
	var body struct {
		IDs     []string `json:"ids"`
		Enabled bool     `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}
	if len(body.IDs) == 0 {
		writeAppError(w, r, apperr.BadRequest("ids must not be empty"))
		return
	}
	if len(body.IDs) > 200 {
		writeAppError(w, r, apperr.BadRequest("too many ids (max 200)"))
		return
	}

	results := make([]batchItemResult, 0, len(body.IDs))
	okCount := 0
	changed := false

	for _, id := range body.IDs {
		// We can't easily peek the current enabled flag without a
		// second round trip per id; SetEnabled returns ErrNotFound if
		// the id doesn't exist (and RowsAffected=0 otherwise). For
		// idempotency we treat "no rows affected" as a not-found /
		// already-in-state marker and skip the audit on the happy path.
		// Idempotency check: list once, compare, then SetEnabled.
		skills, err := h.Repo.List(r.Context())
		if err != nil {
			results = append(results, batchItemResult{ID: id, Status: "error", Error: err.Error()})
			continue
		}
		var current *model.Skill
		for i := range skills {
			if skills[i].ID == id {
				current = &skills[i]
				break
			}
		}
		if current == nil {
			results = append(results, batchItemResult{ID: id, Status: "not_found"})
			continue
		}
		if current.Enabled == body.Enabled {
			results = append(results, batchItemResult{ID: id, Status: "unchanged"})
			continue
		}
		if err := h.Repo.SetEnabled(r.Context(), id, body.Enabled); err != nil {
			if errors.Is(err, skill.ErrNotFound) {
				results = append(results, batchItemResult{ID: id, Status: "not_found"})
				continue
			}
			results = append(results, batchItemResult{ID: id, Status: "error", Error: err.Error()})
			continue
		}
		results = append(results, batchItemResult{ID: id, Status: "ok"})
		okCount++
		changed = true
		if h.AuditSink != nil {
			h.AuditSink.EmitFromRequest(r, audit.ActionSkillToggle, "skill", id,
				audit.Payload{"enabled": body.Enabled})
		}
	}

	// Reload registry once if we changed anything.
	if changed {
		if err := h.reload(r.Context()); err != nil {
			lg.Warn().Err(err).Msg("skill_admin_reload_failed_after_toggle_batch")
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total":     len(body.IDs),
		"succeeded": okCount,
		"enabled":   body.Enabled,
		"results":   results,
	})
}

// keep model import referenced.
var _ = model.Skill{}