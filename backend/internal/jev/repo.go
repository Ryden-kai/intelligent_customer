// Package jev — repo.go
//
// DB CRUD for jev_templates + jev_decisions. The two tables back the
// admin templates page and the observability page respectively. We
// keep them in one file because they share the same go.mod module and
// the JSON serialization quirks are aligned.

package jev

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"intelligent_customer/backend/internal/apperr"
)

// ----- jev_templates CRUD ----------------------------------------------------

// TemplateRecord is the DB row shape for jev_templates. Wider than
// Template because it includes the audit fields admin pages need.
type TemplateRecord struct {
	ID             string       `json:"id"`
	TenantID       string       `json:"tenantId"`
	Name           string       `json:"name"`
	Version        int          `json:"version"`
	Trigger        TriggerPoint `json:"trigger"`
	OutputType     OutputType   `json:"outputType"`
	Labels         []string     `json:"labels"`
	Instructions   string       `json:"instructions"`
	Fallback       FallbackSpec `json:"fallback"`
	DefinitionYAML string       `json:"definitionYaml"`
	Status         string       `json:"status"` // draft | pending | approved | published | archived
	IndustryCode   string       `json:"industryCode,omitempty"`
	CreatedBy      string       `json:"createdBy,omitempty"`
	CreatedAt      time.Time    `json:"createdAt"`
	PublishedAt    *time.Time   `json:"publishedAt,omitempty"`
	ArchivedAt     *time.Time   `json:"archivedAt,omitempty"`
}

// TemplateRepo owns jev_templates CRUD. All methods are tenant-scoped:
// callers must supply tenantID (typically from ctx via tenant.FromContext).
type TemplateRepo struct {
	DB *sql.DB
}

func NewTemplateRepo(db *sql.DB) *TemplateRepo { return &TemplateRepo{DB: db} }

// ErrTemplateNotFound is returned when Get misses. Distinct from
// ErrDecisionNotFound so callers can branch.
var ErrTemplateNotFound = errors.New("jev template not found")

// ErrTemplateVersionConflict surfaces a duplicate (tenant, name, version).
var ErrTemplateVersionConflict = errors.New("jev template version conflict")

// Create inserts a new template version. The (tenant, name, version)
// UNIQUE constraint is enforced by the DB; we map the error to
// ErrTemplateVersionConflict.
func (r *TemplateRepo) Create(ctx context.Context, t *TemplateRecord) error {
	if t.TenantID == "" || t.Name == "" {
		return apperr.BadRequest("tenant_id and name required")
	}
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	now := time.Now()
	t.CreatedAt = now
	if t.Status == "" {
		t.Status = "draft"
	}
	if t.Version <= 0 {
		t.Version = 1
	}
	labelsJSON, _ := json.Marshal(t.Labels)
	fallbackJSON, _ := json.Marshal(t.Fallback)
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO jev_templates(id, tenant_id, name, version, trigger, output_type,
		                           labels_json, instructions, fallback_json, definition_yaml,
		                           status, industry_code, created_by, created_at, published_at, archived_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.TenantID, t.Name, t.Version, string(t.Trigger), string(t.OutputType),
		string(labelsJSON), t.Instructions, string(fallbackJSON), t.DefinitionYAML,
		t.Status, nullableString(t.IndustryCode), nullableString(t.CreatedBy),
		t.CreatedAt.UnixMilli(),
		nullableMillis(t.PublishedAt), nullableMillis(t.ArchivedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return apperr.Conflict(fmt.Errorf("%w: %s/%s/v%d", ErrTemplateVersionConflict, t.TenantID, t.Name, t.Version).Error())
		}
		return err
	}
	return nil
}

// Get loads a single template row by id.
func (r *TemplateRepo) Get(ctx context.Context, id string) (*TemplateRecord, error) {
	row := r.DB.QueryRowContext(ctx,
		`SELECT id, tenant_id, name, version, trigger, output_type,
		        labels_json, instructions, fallback_json, definition_yaml,
		        status, industry_code, created_by, created_at, published_at, archived_at
		 FROM jev_templates WHERE id=?`, id)
	return scanTemplate(row)
}

// ListByTenant returns all templates for a tenant, newest first.
func (r *TemplateRepo) ListByTenant(ctx context.Context, tenantID string) ([]TemplateRecord, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT id, tenant_id, name, version, trigger, output_type,
		        labels_json, instructions, fallback_json, definition_yaml,
		        status, industry_code, created_by, created_at, published_at, archived_at
		 FROM jev_templates WHERE tenant_id=? ORDER BY name ASC, version DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TemplateRecord, 0)
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// Publish flips status to 'published' and stamps published_at. Idempotent.
func (r *TemplateRepo) Publish(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()
	res, err := r.DB.ExecContext(ctx,
		`UPDATE jev_templates SET status='published', published_at=? WHERE id=? AND status IN ('draft','approved','published')`,
		now, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTemplateNotFound
	}
	return nil
}

// Archive flips status to 'archived' and stamps archived_at. Idempotent.
func (r *TemplateRepo) Archive(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()
	res, err := r.DB.ExecContext(ctx,
		`UPDATE jev_templates SET status='archived', archived_at=? WHERE id=?`,
		now, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTemplateNotFound
	}
	return nil
}

// Delete removes a template row. Used by admin endpoints; default
// tenant templates are NOT protected here (admin can wipe them) —
// v2.2 will add a guard.
func (r *TemplateRepo) Delete(ctx context.Context, id string) error {
	res, err := r.DB.ExecContext(ctx, `DELETE FROM jev_templates WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTemplateNotFound
	}
	return nil
}

// ----- jev_decisions CRUD ----------------------------------------------------

// DecisionRecord is the DB row shape for jev_decisions. The full
// input/output JSON is preserved so admins can audit exactly what the
// model saw vs. what it returned.
type DecisionRecord struct {
	ID              string       `json:"id"`
	TenantID        string       `json:"tenantId"`
	TemplateName    string       `json:"templateName"`
	TemplateVersion int          `json:"templateVersion"`
	Trigger         TriggerPoint `json:"trigger"`
	InputHash       string       `json:"inputHash"`
	InputJSON       string       `json:"inputJson"`
	OutputJSON      string       `json:"outputJson"`
	Fallback        bool         `json:"fallback"`
	Confidence      *float64     `json:"confidence,omitempty"`
	LatencyMS       int          `json:"latencyMs"`
	CostUSD         float64      `json:"costUsd"`
	Status          string       `json:"status"` // decided | reviewed | accepted | rejected
	GroundTruth     string       `json:"groundTruth,omitempty"`
	ReviewedBy      string       `json:"reviewedBy,omitempty"`
	ReviewedAt      *time.Time   `json:"reviewedAt,omitempty"`
	TraceID         string       `json:"traceId"`
	CreatedAt       time.Time    `json:"createdAt"`
}

// DecisionRepo owns jev_decisions CRUD.
type DecisionRepo struct {
	DB *sql.DB
}

func NewDecisionRepo(db *sql.DB) *DecisionRepo { return &DecisionRepo{DB: db} }

// ErrDecisionNotFound is returned when Get misses.
var ErrDecisionNotFound = errors.New("jev decision not found")

// Insert writes a single decision. The caller pre-populates ID +
// timestamps; we just marshal/unmarshal the JSON fields.
func (r *DecisionRepo) Insert(ctx context.Context, d *DecisionRecord) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	if d.Status == "" {
		d.Status = "decided"
	}
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO jev_decisions(id, tenant_id, template_name, template_version, trigger,
		                           input_hash, input_json, output_json, fallback, confidence,
		                           latency_ms, cost_usd, status, ground_truth,
		                           reviewed_by, reviewed_at, trace_id, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.TenantID, d.TemplateName, d.TemplateVersion, string(d.Trigger),
		d.InputHash, d.InputJSON, d.OutputJSON, boolInt(d.Fallback), nullableFloat(d.Confidence),
		d.LatencyMS, d.CostUSD, d.Status, nullableString(d.GroundTruth),
		nullableString(d.ReviewedBy), nullableMillis(d.ReviewedAt), d.TraceID,
		d.CreatedAt.UnixMilli())
	return err
}

// Get loads a decision by id.
func (r *DecisionRepo) Get(ctx context.Context, id string) (*DecisionRecord, error) {
	row := r.DB.QueryRowContext(ctx,
		`SELECT id, tenant_id, template_name, template_version, trigger,
		        input_hash, input_json, output_json, fallback, confidence,
		        latency_ms, cost_usd, status, ground_truth,
		        reviewed_by, reviewed_at, trace_id, created_at
		 FROM jev_decisions WHERE id=?`, id)
	return scanDecision(row)
}

// DecisionFilter narrows List for the admin observability page.
type DecisionFilter struct {
	TenantID     string
	TemplateName string
	Trigger      TriggerPoint
	Status       string
	Limit        int
	Offset       int
}

// List returns a paginated slice of decisions, newest first.
func (r *DecisionRepo) List(ctx context.Context, f DecisionFilter) ([]DecisionRecord, int, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args := []any{f.TenantID}
	where := "tenant_id = ?"
	if f.TemplateName != "" {
		where += " AND template_name = ?"
		args = append(args, f.TemplateName)
	}
	if f.Trigger != "" {
		where += " AND trigger = ?"
		args = append(args, string(f.Trigger))
	}
	if f.Status != "" {
		where += " AND status = ?"
		args = append(args, f.Status)
	}
	var total int
	if err := r.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jev_decisions WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := r.DB.QueryContext(ctx,
		`SELECT id, tenant_id, template_name, template_version, trigger,
		        input_hash, input_json, output_json, fallback, confidence,
		        latency_ms, cost_usd, status, ground_truth,
		        reviewed_by, reviewed_at, trace_id, created_at
		 FROM jev_decisions WHERE `+where+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]DecisionRecord, 0)
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *d)
	}
	return out, total, rows.Err()
}

// Stats is the rolled-up observability payload for the dashboard.
type Stats struct {
	Total        int     `json:"total"`
	FallbackRate float64 `json:"fallbackRate"`
	P95LatencyMS int     `json:"p95LatencyMs"`
	ByTemplate   []TemplateStat `json:"byTemplate"`
}

type TemplateStat struct {
	Template string  `json:"template"`
	Count    int     `json:"count"`
	Fallback int     `json:"fallback"`
	Ratio    float64 `json:"fallbackRatio"`
}

// Stats rolls up the past-N-days decisions for the admin dashboard.
// Default window is 7 days; callers pass a custom one for ad-hoc queries.
func (r *DecisionRepo) Stats(ctx context.Context, tenantID string, window time.Duration) (*Stats, error) {
	if window <= 0 {
		window = 7 * 24 * time.Hour
	}
	since := time.Now().Add(-window).UnixMilli()
	out := &Stats{}
	rows, err := r.DB.QueryContext(ctx,
		`SELECT template_name, COUNT(*), SUM(fallback), latency_ms
		 FROM jev_decisions
		 WHERE tenant_id=? AND created_at >= ?
		 ORDER BY created_at DESC`, tenantID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type accum struct {
		count, fallback int
		latencies       []int
	}
	per := map[string]*accum{}
	for rows.Next() {
		var name string
		var count, fallback int
		var latency int
		if err := rows.Scan(&name, &count, &fallback, &latency); err != nil {
			return nil, err
		}
		a, ok := per[name]
		if !ok {
			a = &accum{}
			per[name] = a
		}
		a.count += count
		a.fallback += fallback
		a.latencies = append(a.latencies, latency)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	all := make([]int, 0)
	for name, a := range per {
		out.Total += a.count
		out.ByTemplate = append(out.ByTemplate, TemplateStat{
			Template: name,
			Count:    a.count,
			Fallback: a.fallback,
			Ratio:    safeRatio(a.fallback, a.count),
		})
		all = append(all, a.latencies...)
	}
	out.FallbackRate = safeRatio(perTemplateFallbackSum(out.ByTemplate), out.Total)
	out.P95LatencyMS = percentile95(all)
	return out, nil
}

func safeRatio(num, denom int) float64 {
	if denom == 0 {
		return 0
	}
	return float64(num) / float64(denom)
}

func perTemplateFallbackSum(rows []TemplateStat) int {
	n := 0
	for _, r := range rows {
		n += r.Fallback
	}
	return n
}

func percentile95(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	// Simple selection: sort + take idx at 95%. Cheap because the
	// window is bounded (we query SQLite directly with a row limit).
	cp := make([]int, len(xs))
	copy(cp, xs)
	// insertion sort — fine for hundreds of rows.
	for i := 1; i < len(cp); i++ {
		for j := i; j > 0 && cp[j-1] > cp[j]; j-- {
			cp[j-1], cp[j] = cp[j], cp[j-1]
		}
	}
	idx := int(0.95 * float64(len(cp)-1))
	return cp[idx]
}

// MarkReviewed flips status + writes ground_truth. Used by the
// admin /api/admin/jev/decisions/{id}/review endpoint and by the
// satisfaction feedback pipeline.
func (r *DecisionRepo) MarkReviewed(ctx context.Context, id, status, groundTruth, reviewer string) error {
	if status != "accepted" && status != "rejected" {
		return apperr.BadRequest("status must be accepted or rejected")
	}
	now := time.Now().UnixMilli()
	res, err := r.DB.ExecContext(ctx,
		`UPDATE jev_decisions SET status=?, ground_truth=?, reviewed_by=?, reviewed_at=?
		 WHERE id=?`, status, groundTruth, reviewer, now, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrDecisionNotFound
	}
	return nil
}

// ----- internal helpers ------------------------------------------------------

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableMillis(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

func nullableFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// modernc.org/sqlite returns plain string errors. The pattern
	// "UNIQUE constraint failed" is stable across versions.
	msg := err.Error()
	return contains(msg, "UNIQUE constraint failed")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	if sub == "" {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ----- scan helpers ----------------------------------------------------------

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTemplate(s rowScanner) (*TemplateRecord, error) {
	var (
		t        TemplateRecord
		labels   sql.NullString
		fb       sql.NullString
		def      sql.NullString
		industry sql.NullString
		creator  sql.NullString
		pubAt    sql.NullInt64
		arcAt    sql.NullInt64
		created  int64
	)
	if err := s.Scan(
		&t.ID, &t.TenantID, &t.Name, &t.Version, &t.Trigger, &t.OutputType,
		&labels, &t.Instructions, &fb, &def,
		&t.Status, &industry, &creator, &created, &pubAt, &arcAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTemplateNotFound
		}
		return nil, err
	}
	if labels.Valid && labels.String != "" {
		_ = json.Unmarshal([]byte(labels.String), &t.Labels)
	}
	if fb.Valid && fb.String != "" {
		_ = json.Unmarshal([]byte(fb.String), &t.Fallback)
	}
	if def.Valid {
		t.DefinitionYAML = def.String
	}
	if industry.Valid {
		t.IndustryCode = industry.String
	}
	if creator.Valid {
		t.CreatedBy = creator.String
	}
	if pubAt.Valid {
		ts := time.UnixMilli(pubAt.Int64)
		t.PublishedAt = &ts
	}
	if arcAt.Valid {
		ts := time.UnixMilli(arcAt.Int64)
		t.ArchivedAt = &ts
	}
	t.CreatedAt = time.UnixMilli(created)
	return &t, nil
}

func scanDecision(s rowScanner) (*DecisionRecord, error) {
	var (
		d          DecisionRecord
		trigger    string
		fallback   int
		confidence sql.NullFloat64
		ground     sql.NullString
		reviewer   sql.NullString
		reviewedAt sql.NullInt64
		created    int64
	)
	if err := s.Scan(
		&d.ID, &d.TenantID, &d.TemplateName, &d.TemplateVersion, &trigger,
		&d.InputHash, &d.InputJSON, &d.OutputJSON, &fallback, &confidence,
		&d.LatencyMS, &d.CostUSD, &d.Status, &ground,
		&reviewer, &reviewedAt, &d.TraceID, &created,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDecisionNotFound
		}
		return nil, err
	}
	d.Trigger = TriggerPoint(trigger)
	d.Fallback = fallback == 1
	if confidence.Valid {
		v := confidence.Float64
		d.Confidence = &v
	}
	if ground.Valid {
		d.GroundTruth = ground.String
	}
	if reviewer.Valid {
		d.ReviewedBy = reviewer.String
	}
	if reviewedAt.Valid {
		ts := time.UnixMilli(reviewedAt.Int64)
		d.ReviewedAt = &ts
	}
	d.CreatedAt = time.UnixMilli(created)
	return &d, nil
}
