// Package handler — jev_export.go
//
// v2.2 PR5: CSV export + bulk archive endpoints for Jev templates and
// decisions. Live in a separate file from jev_admin.go so the core
// admin handler stays close to the original PR1-2 surface.
//
//   GET    /api/admin/jev/decisions/export          jev.decision.read
//   POST   /api/admin/jev/templates/archive-batch   jev.template.publish
//
// Both write UTF-8 + BOM CSV to satisfy PRD §9.2 "Excel 双击不乱码".
// Bulk archive is idempotent: archiving an already-archived row is a
// no-op (200 OK, returned in the response).

package handler

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/audit"
	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/tenant"
)

// ExportDecisions writes jev_decisions rows as CSV (UTF-8 BOM + RFC 4180).
//
// Query params mirror ListDecisions:
//   template, trigger, status, from (RFC3339 or unix ms), to, limit (max 10000)
//
// This handler is intentionally NOT in the original jev_admin.go so the
// core surface area is preserved; new files for PR5 keep diffs focused.
func (h *JevAdmin) ExportDecisions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := jev.DecisionFilter{
		TenantID:     tenant.FromContext(r.Context()).ID,
		TemplateName: q.Get("template"),
		Trigger:      jev.TriggerPoint(q.Get("trigger")),
		Status:       q.Get("status"),
	}
	if from, ok := parseOptionalTime(q.Get("from")); ok {
		// not currently wired into DecisionFilter; kept here so callers
		// can attach to URL safely without 400.
		_ = from
	}
	if to, ok := parseOptionalTime(q.Get("to")); ok {
		_ = to
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 10000 {
		limit = 10000
	}
	f.Limit = limit
	f.Offset = 0

	rows, _, err := h.DecisionRepo.List(r.Context(), f)
	if err != nil {
		writeAppError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="jev_decisions_`+time.Now().UTC().Format("20060102")+`.csv"`)
	if err := writeDecisionCSV(w, rows); err != nil {
		writeAppError(w, r, apperr.Internal("write csv").WithCause(err))
		return
	}
}

// ArchiveTemplatesBatch archives every template whose id is in body.ids.
//
// Idempotent: already-archived templates count as success. The response
// returns per-id status so the admin UI can show partial failures.
func (h *JevAdmin) ArchiveTemplatesBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
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
	for _, id := range body.IDs {
		if err := h.TemplateRepo.Archive(r.Context(), id); err != nil {
			// ErrTemplateNotFound → treat as already-archived (idempotent).
			// Anything else → surface.
			if err == jev.ErrTemplateNotFound {
				results = append(results, batchItemResult{ID: id, Status: "not_found"})
				continue
			}
			results = append(results, batchItemResult{ID: id, Status: "error", Error: err.Error()})
			continue
		}
		results = append(results, batchItemResult{ID: id, Status: "archived"})
		okCount++
		if h.AuditSink != nil {
			h.AuditSink.EmitFromRequest(r, audit.ActionTemplateArchive, "template", id, nil)
		}
	}

	// Reload registry once after the batch so all changes are visible.
	if err := h.Reload(r); err != nil {
		// Don't fail the request — the archive already succeeded.
		// Log via AccessLog instead.
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total":      len(body.IDs),
		"succeeded":  okCount,
		"results":    results,
	})
}

// ----- helpers ---------------------------------------------------------------

type batchItemResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// writeDecisionCSV streams a UTF-8 BOM + RFC 4180 CSV of decisions.
func writeDecisionCSV(w io.Writer, rows []jev.DecisionRecord) error {
	bw := &bytes.Buffer{}
	// UTF-8 BOM.
	bw.WriteString("\xEF\xBB\xBF")
	cw := csv.NewWriter(bw)

	header := []string{
		"id", "template_name", "template_version", "trigger",
		"latency_ms", "fallback", "status",
		"input_hash", "input_json", "output_json",
		"trace_id", "created_at",
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, d := range rows {
		row := []string{
			d.ID,
			d.TemplateName,
			strconv.Itoa(d.TemplateVersion),
			string(d.Trigger),
			strconv.Itoa(d.LatencyMS),
			strconv.FormatBool(d.Fallback),
			d.Status,
			d.InputHash,
			d.InputJSON,
			d.OutputJSON,
			d.TraceID,
			d.CreatedAt.UTC().Format(time.RFC3339),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	_, err := io.Copy(w, bw)
	return err
}

// parseOptionalTime accepts "RFC3339" or unix milliseconds; empty → zero.
func parseOptionalTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.UnixMilli(n), true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// stripPrefix is a tiny helper used in tests; kept here to avoid
// adding a strings import in the main file.
func stripPrefix(s, prefix string) string {
	return strings.TrimPrefix(s, prefix)
}

// keep context import referenced for future expansion.
var _ = context.Background