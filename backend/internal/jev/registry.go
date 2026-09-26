// Package jev — registry.go
//
// Template registry for the Jev decision layer. Templates are merged
// from three sources in priority order (highest first):
//
//  1. Database rows in jev_templates WHERE status='published'
//  2. YAML files under backend/templates/jev/*.yaml
//  3. Built-in defaults compiled into the binary (fallback for the
//     five v2.1 scenarios)
//
// All templates are keyed by (tenantID, trigger). When a tenant lookup
// misses, we walk the chain above starting from the tenant level then
// fall back to the DefaultTenant (tnt_default). Built-in defaults
// cover the last-resort case so a fresh install can serve requests
// without any DB rows or YAML files configured.
//
// Reload is explicit — calling LoadFrom* on a live registry swaps the
// in-memory map atomically (under a mutex). Concurrent reads during
// reload see either the old or the new snapshot, never a half-built
// one.

package jev

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/tenant"
)

// TriggerPoint names the slot in the system that issued the decision.
// Six slots match docs/v2-jev-rollout.md §2.2.
type TriggerPoint string

const (
	TriggerMessageEntry TriggerPoint = "message.entry"      // ① 意图路由
	TriggerPreIngest    TriggerPoint = "message.pre_ingest" // ⑤ 敏感词（用户消息入库前）
	TriggerPreReply     TriggerPoint = "reply.pre_ingest"   // ⑤ 敏感词（Agent 回复入库前）
	TriggerPreHandover  TriggerPoint = "handover.pre"       // ⑦ 情绪
	TriggerTicketCreate TriggerPoint = "ticket.create"      // ④ 工单优先级
	TriggerAgentAssign  TriggerPoint = "agent.assign"       // ⑥ 坐席分配
)

// AllTriggers is the canonical list — used by tests + admin endpoints
// to enumerate available slots.
var AllTriggers = []TriggerPoint{
	TriggerMessageEntry, TriggerPreIngest, TriggerPreReply,
	TriggerPreHandover, TriggerTicketCreate, TriggerAgentAssign,
}

// OutputType maps to OpenAI Decisions API question.type.
type OutputType string

const (
	OutputChoice OutputType = "choice"
	OutputScore  OutputType = "score"
	OutputNoul   OutputType = "noul"
)

// FallbackSpec describes what to return when the live Jev call fails.
// v2.1 only supports static defaults ("type: default"); v2.3 can extend
// with "type: local_rule" pointing at a YAML in data/jev_rules/.
type FallbackSpec struct {
	Type  string         `yaml:"type" json:"type"`
	Value string         `yaml:"value,omitempty" json:"value,omitempty"`
	Score map[string]any `yaml:"score,omitempty" json:"score,omitempty"`
}

// Template is the runtime shape used by the Orchestrator. definition_yaml
// is preserved verbatim so admin UIs can show the source.
type Template struct {
	Name           string       `yaml:"name" json:"name"`
	Version        int          `yaml:"version" json:"version"`
	Description    string       `yaml:"description" json:"description"`
	Trigger        TriggerPoint `yaml:"trigger" json:"trigger"`
	OutputType     OutputType   `yaml:"output_type" json:"outputType"`
	Labels         []string     `yaml:"labels,omitempty" json:"labels,omitempty"`
	Instructions   string       `yaml:"template" json:"instructions"` // text/template body
	Fallback       FallbackSpec `yaml:"fallback" json:"fallback"`
	DefinitionYAML string       `yaml:"-" json:"definitionYaml"` // raw bytes after parse
}

// Validate enforces minimum fields. Used by admin endpoints before
// publishing a new template version.
func (t Template) Validate() error {
	if t.Name == "" {
		return errors.New("template name required")
	}
	if t.Trigger == "" {
		return errors.New("template trigger required")
	}
	switch t.OutputType {
	case OutputChoice, OutputScore, OutputNoul:
	default:
		return fmt.Errorf("invalid output_type %q", t.OutputType)
	}
	if t.Instructions == "" {
		return errors.New("template body required")
	}
	if t.OutputType == OutputChoice && len(t.Labels) == 0 {
		return errors.New("choice templates need at least one label")
	}
	if t.Fallback.Type == "" {
		t.Fallback.Type = "default"
	}
	return nil
}

// Registry is the in-memory lookup table. Safe for concurrent reads;
// writes go through Reload which swaps the whole table atomically.
type Registry struct {
	mu sync.RWMutex
	// keyed by tenantID (or "default" for built-ins) → trigger → []Template
	byTrigger map[string]map[TriggerPoint][]Template
	// raw YAMLs kept around so admin endpoints can stream them back.
	byName map[string]map[string]Template // tenantID → name → Template (latest version only)
	// lastReload tracks when LoadFrom* last swapped the table; exposed
	// for the admin /api/admin/jev/templates endpoint and for tests.
	lastReload time.Time
}

// NewRegistry returns an empty registry with built-in defaults pre-loaded
// under the synthetic "default" tenant. Callers should immediately
// LoadFromFS / LoadFromDB to overlay tenant-specific templates.
func NewRegistry() *Registry {
	r := &Registry{
		byTrigger: make(map[string]map[TriggerPoint][]Template),
		byName:    make(map[string]map[string]Template),
	}
	for _, t := range builtinTemplates() {
		r.putLocked("default", t)
	}
	r.lastReload = time.Now()
	return r
}

// LoadFromFS reads *.yaml files under dir into the registry. Files that
// fail to parse or validate are skipped with a returned error slice —
// the call site decides whether to fail hard or log+continue.
func (r *Registry) LoadFromFS(dir string) (int, []error) {
	if dir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, []error{fmt.Errorf("read fs dir %s: %w", dir, err)}
	}
	var loadErrs []error
	loaded := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			loadErrs = append(loadErrs, fmt.Errorf("read %s: %w", path, err))
			continue
		}
		var t Template
		if err := yaml.Unmarshal(raw, &t); err != nil {
			loadErrs = append(loadErrs, fmt.Errorf("parse %s: %w", path, err))
			continue
		}
		t.DefinitionYAML = string(raw)
		if err := t.Validate(); err != nil {
			loadErrs = append(loadErrs, fmt.Errorf("validate %s: %w", path, err))
			continue
		}
		// FS templates are owned by the platform; they live under
		// the synthetic "default" tenant so DB-published versions
		// can override per tenant.
		r.put("default", t)
		loaded++
	}
	r.mu.Lock()
	r.lastReload = time.Now()
	r.mu.Unlock()
	return loaded, loadErrs
}

// LoadFromDB merges published template rows into the registry. DB rows
// override FS templates of the same (tenant, name). Returns the count
// of successfully loaded templates.
func (r *Registry) LoadFromDB(ctx context.Context, conn *sql.DB) (int, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT id, tenant_id, name, version, trigger, output_type,
		        labels_json, instructions, fallback_json, definition_yaml
		 FROM jev_templates WHERE status='published'`)
	if err != nil {
		return 0, fmt.Errorf("query jev_templates: %w", err)
	}
	defer rows.Close()
	loaded := 0
	for rows.Next() {
		var (
			id, tenantID, name, trig, outType, labelsJSON, instr string
			fallbackJSON, defYAML                                string
			version                                             int
		)
		if err := rows.Scan(&id, &tenantID, &name, &version, &trig, &outType,
			&labelsJSON, &instr, &fallbackJSON, &defYAML); err != nil {
			return loaded, fmt.Errorf("scan: %w", err)
		}
		t := Template{
			Name:           name,
			Version:        version,
			Trigger:        TriggerPoint(trig),
			OutputType:     OutputType(outType),
			Instructions:   instr,
			DefinitionYAML: defYAML,
		}
		if labelsJSON != "" && labelsJSON != "[]" {
			if err := decodeStringSlice(labelsJSON, &t.Labels); err != nil {
				return loaded, fmt.Errorf("decode labels for %s: %w", name, err)
			}
		}
		if err := decodeFallback(fallbackJSON, &t.Fallback); err != nil {
			return loaded, fmt.Errorf("decode fallback for %s: %w", name, err)
		}
		if err := t.Validate(); err != nil {
			return loaded, fmt.Errorf("validate %s: %w", name, err)
		}
		r.put(tenantID, t)
		loaded++
	}
	return loaded, rows.Err()
}

// put is the public, locking variant. Callers without the lock should
// use this; callers already holding the lock should call putLocked.
func (r *Registry) put(tenantID string, t Template) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.putLocked(tenantID, t)
}

// putLocked is the unlocked variant — call only when mu is already held.
func (r *Registry) putLocked(tenantID string, t Template) {
	if r.byTrigger[tenantID] == nil {
		r.byTrigger[tenantID] = make(map[TriggerPoint][]Template)
	}
	if r.byName[tenantID] == nil {
		r.byName[tenantID] = make(map[string]Template)
	}
	r.byName[tenantID][t.Name] = t
	r.byTrigger[tenantID][t.Trigger] = append(
		r.byTrigger[tenantID][t.Trigger], t)
	// Keep only the highest version per (tenant, trigger, name) so the
	// lookup in Get() doesn't see duplicates.
	r.byTrigger[tenantID][t.Trigger] = dedupeHighestVersion(
		r.byTrigger[tenantID][t.Trigger])
}

// Get returns the template for (tenantID, trigger). Lookup order:
//   1. tenantID-specific match
//   2. "default" tenant (FS + built-ins)
// Returns apperr.NotFound when nothing matches.
func (r *Registry) Get(tenantID string, trigger TriggerPoint) (*Template, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, key := range []string{tenantID, "default"} {
		if ts, ok := r.byTrigger[key][trigger]; ok && len(ts) > 0 {
			// Pick the highest version deterministically.
			best := ts[0]
			for _, cand := range ts[1:] {
				if cand.Version > best.Version {
					best = cand
				}
			}
			out := best
			return &out, nil
		}
	}
	return nil, apperr.NotFound(fmt.Sprintf("no jev template for tenant=%s trigger=%s", tenantID, trigger))
}

// ListByTenant returns a snapshot of all templates visible to tenantID
// (own + "default"). Used by admin endpoints.
func (r *Registry) ListByTenant(tenantID string) []Template {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := map[string]struct{}{}
	out := make([]Template, 0)
	for _, key := range []string{tenantID, "default"} {
		for _, t := range r.byName[key] {
			if _, dup := seen[t.Name]; dup {
				continue
			}
			seen[t.Name] = struct{}{}
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Reset clears everything but does NOT reinstall built-ins. Used by
// tests that need to verify behaviour with an empty registry.
// NewRegistry already installs built-ins; tests that want them back
// should construct a fresh registry.
func (r *Registry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byTrigger = make(map[string]map[TriggerPoint][]Template)
	r.byName = make(map[string]map[string]Template)
	r.lastReload = time.Now()
}

// dedupeHighestVersion keeps only the highest-version entry per Name.
// Stable for tests (sort by Version desc, then Name asc).
func dedupeHighestVersion(in []Template) []Template {
	byName := map[string]Template{}
	for _, t := range in {
		cur, ok := byName[t.Name]
		if !ok || t.Version > cur.Version {
			byName[t.Name] = t
		}
	}
	out := make([]Template, 0, len(byName))
	for _, t := range rangeTemplates(byName) {
		out = append(out, t)
	}
	return out
}

func rangeTemplates(m map[string]Template) []Template {
	out := make([]Template, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// builtinTemplates returns the hard-coded defaults. Kept small + generic
// so a fresh install can serve requests without any DB or FS config.
// Each one matches one of the v2.1 decision triggers.
func builtinTemplates() []Template {
	return []Template{
		{
			Name:         "intent_routing",
			Version:      1,
			Description:  "Default intent classifier for inbound user messages.",
			Trigger:      TriggerMessageEntry,
			OutputType:   OutputChoice,
			Labels:       []string{"order", "refund", "coupon", "faq", "chitchat", "sensitive", "complaint", "out_of_scope"},
			Instructions: "判断用户当前这条消息的咨询意图，从 label 中选择最匹配的一个。\n用户消息：{{.message}}",
			Fallback:     FallbackSpec{Type: "default", Value: "chitchat"},
		},
		{
			Name:         "sensitive_check",
			Version:      1,
			Description:  "Default sensitive / compliance check on inbound + outbound text.",
			Trigger:      TriggerPreIngest,
			OutputType:   OutputChoice,
			Labels:       []string{"clean", "sensitive", "violate"},
			Instructions: "判断下列内容是否包含敏感或违规信息。\n内容：{{.content}}",
			Fallback:     FallbackSpec{Type: "conservative", Value: "sensitive"},
		},
		{
			Name:         "emotion_detection",
			Version:      1,
			Description:  "Default emotion + urgency scorer (handover pre-check).",
			Trigger:      TriggerPreHandover,
			OutputType:   OutputScore,
			Instructions: "分析用户最近消息的情绪分布与紧急度。\n最近消息：{{.recent_messages}}\n输出 JSON：{\"scores\":{\"angry\":0.x,\"anxious\":0.x,\"neutral\":0.x,\"urgent\":0.x}}",
			Fallback:     FallbackSpec{Type: "default", Value: "neutral", Score: map[string]any{"neutral": 1.0}},
		},
		{
			Name:         "ticket_priority",
			Version:      1,
			Description:  "Default ticket priority classifier for newly created tickets.",
			Trigger:      TriggerTicketCreate,
			OutputType:   OutputChoice,
			Labels:       []string{"p0", "p1", "p2", "p3"},
			Instructions: "判断工单的紧急度。\n工单描述：{{.description}}\n客户等级：{{.customer_tier}}",
			Fallback:     FallbackSpec{Type: "default", Value: "p2"},
		},
		{
			Name:         "agent_routing",
			Version:      1,
			Description:  "Default agent skill-group routing decision.",
			Trigger:      TriggerAgentAssign,
			OutputType:   OutputChoice,
			Labels:       []string{"default_skill_group", "vip_skill_group", "tech_skill_group"},
			Instructions: "判断转人工会话应该路由到哪个技能组。\n可用技能组：{{.skill_groups}}\n技能组负载：{{.skill_group_load}}",
			Fallback:     FallbackSpec{Type: "default", Value: "default_skill_group"},
		},
	}
}

// decodeFallback / decodeStringSlice are tiny JSON helpers kept here so
// callers don't have to import encoding/json themselves. The YAML
// loader uses them indirectly when reading JSON-encoded columns.
func decodeFallback(raw string, out *FallbackSpec) error {
	if raw == "" || raw == "{}" {
		return nil
	}
	return yaml.Unmarshal([]byte(raw), out)
}

func decodeStringSlice(raw string, out *[]string) error {
	if raw == "" || raw == "[]" {
		return nil
	}
	return yaml.Unmarshal([]byte(raw), out)
}

// LoadFromDefaultTenant loads tnt_default's templates into the registry
// under the DefaultID key. Convenience for the seed path.
func (r *Registry) LoadFromDefaultTenant(ctx context.Context, conn *sql.DB) error {
	_, err := r.LoadFromDBFor(ctx, conn, tenant.DefaultID)
	return err
}

// LoadFromDBFor mirrors LoadFromDB but filters by tenant_id. Used by the
// per-tenant reload endpoint in v2.2; v2.1 keeps it for symmetry.
func (r *Registry) LoadFromDBFor(ctx context.Context, conn *sql.DB, tenantID string) (int, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT id, name, version, trigger, output_type,
		        labels_json, instructions, fallback_json, definition_yaml
		 FROM jev_templates WHERE status='published' AND tenant_id=?`, tenantID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	loaded := 0
	for rows.Next() {
		var (
			id, name, trig, outType, labelsJSON, instr, fallbackJSON, defYAML string
			version                                                            int
		)
		if err := rows.Scan(&id, &name, &version, &trig, &outType,
			&labelsJSON, &instr, &fallbackJSON, &defYAML); err != nil {
			return loaded, err
		}
		t := Template{
			Name:           name,
			Version:        version,
			Trigger:        TriggerPoint(trig),
			OutputType:     OutputType(outType),
			Instructions:   instr,
			DefinitionYAML: defYAML,
		}
		if labelsJSON != "" && labelsJSON != "[]" {
			_ = decodeStringSlice(labelsJSON, &t.Labels)
		}
		_ = decodeFallback(fallbackJSON, &t.Fallback)
		if err := t.Validate(); err != nil {
			return loaded, err
		}
		r.put(tenantID, t)
		loaded++
	}
	return loaded, rows.Err()
}

// LastReloadedAt is exposed for tests + observability.
func (r *Registry) LastReloadedAt() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastReload
}
