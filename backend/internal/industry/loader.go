// Package industry owns the industry-template loader. v2.1 ships one
// template ("general_v1") covering the generic ecommerce customer-
// service surface. v2.3 will add "medicine_gsp_v1" once the GSP
// work begins.
//
// Each template is a YAML bundle that captures:
//
//   - the canonical intent label set for that industry
//   - the sensitive-word blacklist
//   - the priority rules for ticket creation
//
// The loader exposes two operations:
//
//   - Load(code)         : read template from FS / DB / builtin
//   - Activate(tenant, code) : materialise the template's intent / sensitive
//                              / priority rules as jev_templates rows for
//                              the given tenant so the orchestrator can
//                              pick them up via Registry.Get.
//
// Activation is idempotent: re-running Activate for the same
// (tenant, code) bumps the version on each derived jev_template row
// rather than duplicating.

package industry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/tenant"
)

// Template is the parsed shape of an industry bundle.
type Template struct {
	Code        string   `yaml:"code" json:"code"`
	Name        string   `yaml:"name" json:"name"`
	Version     int      `yaml:"version" json:"version"`
	Description string   `yaml:"description" json:"description"`
	Intent      IntentSet `yaml:"intent" json:"intent"`
	Sensitive   SensitiveSet `yaml:"sensitive" json:"sensitive"`
	Priority    PrioritySet `yaml:"priority" json:"priority"`
}

// IntentSet is the canonical intent label set for this industry.
type IntentSet struct {
	Labels       []string `yaml:"labels" json:"labels"`
	FallbackLabel string `yaml:"fallback_label" json:"fallbackLabel"`
}

// SensitiveSet lists the sensitive-word blacklist.
type SensitiveSet struct {
	Conservative bool     `yaml:"conservative" json:"conservative"`
	ViolateWords []string `yaml:"violate_words" json:"violateWords"`
	SensitiveWords []string `yaml:"sensitive_words" json:"sensitiveWords"`
}

// PrioritySet lists the keywords that escalate a ticket.
type PrioritySet struct {
	P0Keywords []string `yaml:"p0_keywords" json:"p0Keywords"`
	P1Keywords []string `yaml:"p1_keywords" json:"p1Keywords"`
	P2Keywords []string `yaml:"p2_keywords" json:"p2Keywords"`
	P3Keywords []string `yaml:"p3_keywords" json:"p3Keywords"`
}

// Loader reads industry templates from FS + DB.
type Loader struct {
	mu     sync.RWMutex
	db     *sql.DB
	dir    string
	cache  map[string]*Template // code → latest template
}

// NewLoader constructs a loader. dir is the FS directory holding
// *.yaml industry bundles; pass "" to skip FS loading (tests).
func NewLoader(dir string, db *sql.DB) *Loader {
	l := &Loader{
		db:    db,
		dir:   dir,
		cache: make(map[string]*Template),
	}
	return l
}

// ErrTemplateNotFound is returned by Load when no template matches `code`.
var ErrTemplateNotFound = errors.New("industry template not found")

// Load returns the latest version of the named template. Lookup order:
//   1. In-memory cache (refreshed by LoadFromFS / LoadFromDB)
//   2. Filesystem: <dir>/<code>.yaml
//   3. SQLite: industry_templates table (newest version)
func (l *Loader) Load(ctx context.Context, code string) (*Template, error) {
	l.mu.RLock()
	if t, ok := l.cache[code]; ok {
		l.mu.RUnlock()
		return t, nil
	}
	l.mu.RUnlock()

	// FS first (smaller, deterministic).
	if l.dir != "" {
		path := filepath.Join(l.dir, code+".yaml")
		if raw, err := os.ReadFile(path); err == nil {
			var t Template
			if err := yaml.Unmarshal(raw, &t); err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
			if err := t.Validate(); err != nil {
				return nil, fmt.Errorf("validate %s: %w", path, err)
			}
			l.cache[code] = &t
			return &t, nil
		}
	}

	// Then DB.
	if l.db != nil {
		var raw string
		var version int
		err := l.db.QueryRowContext(ctx,
			`SELECT config_yaml, version FROM industry_templates WHERE code=? ORDER BY version DESC LIMIT 1`,
			code).Scan(&raw, &version)
		if err == nil {
			var t Template
			if err := yaml.Unmarshal([]byte(raw), &t); err != nil {
				return nil, fmt.Errorf("parse db industry_templates[%s]: %w", code, err)
			}
			t.Version = version
			if err := t.Validate(); err != nil {
				return nil, fmt.Errorf("validate %s (db): %w", code, err)
			}
			l.cache[code] = &t
			return &t, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}

	// Builtin fallback.
	if builtin, ok := builtinTemplates()[code]; ok {
		l.cache[code] = &builtin
		return &builtin, nil
	}
	return nil, ErrTemplateNotFound
}

// LoadFromFS scans dir and merges every *.yaml into the cache. Files
// that fail to parse are skipped with an error returned.
func (l *Loader) LoadFromFS(dir string) (int, []error) {
	if dir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, []error{fmt.Errorf("read industry dir %s: %w", dir, err)}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
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
		if err := t.Validate(); err != nil {
			loadErrs = append(loadErrs, fmt.Errorf("validate %s: %w", path, err))
			continue
		}
		l.cache[t.Code] = &t
		loaded++
	}
	return loaded, loadErrs
}

// Activate materialises the template's intent / sensitive / priority
// rules as jev_template rows for the tenant. Returns the number of
// template rows created (== 3 for v2.1: intent_routing, sensitive_check,
// ticket_priority).
//
// Activation is idempotent: when an existing (tenant, name) row is
// found, the new rows take the next version. The lookup happens once
// upfront so we never hit the UNIQUE-constraint conflict path.
func (l *Loader) Activate(ctx context.Context, tenantID, code string, repo *jev.TemplateRepo) (int, error) {
	if tenantID == "" {
		tenantID = tenant.DefaultID
	}
	if repo == nil {
		return 0, errors.New("industry: jev template repo required")
	}
	t, err := l.Load(ctx, code)
	if err != nil {
		return 0, err
	}
	// Resolve next version per name up-front so the loop below doesn't
	// collide with itself (row N=1's version would conflict with the
	// already-inserted row N=0).
	nextIntent, err := l.nextVersionForName(ctx, repo, tenantID, "intent_routing")
	if err != nil {
		return 0, err
	}
	nextSensitive, err := l.nextVersionForName(ctx, repo, tenantID, "sensitive_check")
	if err != nil {
		return 0, err
	}
	nextPriority, err := l.nextVersionForName(ctx, repo, tenantID, "ticket_priority")
	if err != nil {
		return 0, err
	}

	rows, err := l.materialiseRows(tenantID, t)
	if err != nil {
		return 0, err
	}
	// Patch versions for idempotency.
	rows[0].Version = nextIntent
	rows[1].Version = nextSensitive
	rows[2].Version = nextPriority

	for i := range rows {
		if err := repo.Create(ctx, &rows[i]); err != nil {
			return i, err
		}
	}
	return len(rows), nil
}

// materialiseRows converts the IndustryTemplate into 3 jev TemplateRecord
// rows: intent_routing, sensitive_check, ticket_priority. The agent
// routing + emotion detection templates stay as built-in defaults
// (industry-agnostic) so they aren't materialised here.
func (l *Loader) materialiseRows(tenantID string, t *Template) ([]jev.TemplateRecord, error) {
	now := timeNow()
	// The caller is expected to have bumped Version if the same
	// (tenant, name) already has rows. We default to the template's
	// version and let Activate handle bumping.
	intent := jev.TemplateRecord{
		TenantID:     tenantID,
		Name:         "intent_routing",
		Version:      t.Version,
		Trigger:      jev.TriggerMessageEntry,
		OutputType:   jev.OutputChoice,
		Labels:       append([]string{}, t.Intent.Labels...),
		Instructions: renderIntentTemplate(t),
		Fallback:     jev.FallbackSpec{Type: "default", Value: t.Intent.FallbackLabel},
		Status:       "published",
		IndustryCode: t.Code,
		CreatedBy:    "industry_loader",
		PublishedAt:  &now,
	}
	sensitive := jev.TemplateRecord{
		TenantID:     tenantID,
		Name:         "sensitive_check",
		Version:      t.Version,
		Trigger:      jev.TriggerPreIngest,
		OutputType:   jev.OutputChoice,
		Labels:       []string{"clean", "sensitive", "violate"},
		Instructions: renderSensitiveTemplate(t),
		Fallback:     sensitiveFallback(t),
		Status:       "published",
		IndustryCode: t.Code,
		CreatedBy:    "industry_loader",
		PublishedAt:  &now,
	}
	priority := jev.TemplateRecord{
		TenantID:     tenantID,
		Name:         "ticket_priority",
		Version:      t.Version,
		Trigger:      jev.TriggerTicketCreate,
		OutputType:   jev.OutputChoice,
		Labels:       []string{"p0", "p1", "p2", "p3"},
		Instructions: renderPriorityTemplate(t),
		Fallback:     jev.FallbackSpec{Type: "default", Value: "p2"},
		Status:       "published",
		IndustryCode: t.Code,
		CreatedBy:    "industry_loader",
		PublishedAt:  &now,
	}
	return []jev.TemplateRecord{intent, sensitive, priority}, nil
}

// nextVersionForName returns 1 + the highest existing version for
// (tenantID, name). 0 when no rows exist.
func (l *Loader) nextVersionForName(ctx context.Context, repo *jev.TemplateRepo, tenantID, name string) (int, error) {
	rows, err := repo.ListByTenant(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	maxV := 0
	for _, r := range rows {
		if r.Name == name && r.Version > maxV {
			maxV = r.Version
		}
	}
	return maxV + 1, nil
}

// Validate enforces minimum fields.
func (t Template) Validate() error {
	if t.Code == "" {
		return errors.New("industry template code required")
	}
	if t.Name == "" {
		return errors.New("industry template name required")
	}
	if t.Version == 0 {
		t.Version = 1
	}
	if len(t.Intent.Labels) == 0 {
		return errors.New("intent.labels required")
	}
	if t.Intent.FallbackLabel == "" {
		return errors.New("intent.fallback_label required")
	}
	return nil
}

// builtinTemplates returns the hard-coded defaults. Currently just
// "general_v1" — keep the map so future industries slot in here.
func builtinTemplates() map[string]Template {
	general := Template{
		Code:        "general_v1",
		Name:        "通用电商客服",
		Version:     1,
		Description: "通用电商客服模板：覆盖订单/退款/优惠券/FAQ/投诉/敏感词/工单优先级。",
		Intent: IntentSet{
			Labels: []string{
				"order", "refund", "coupon", "faq",
				"chitchat", "sensitive", "complaint", "out_of_scope",
			},
			FallbackLabel: "chitchat",
		},
		Sensitive: SensitiveSet{
			Conservative: true,
			ViolateWords: []string{"去死", "杀", "诈骗", "毒品", "色情"},
			SensitiveWords: []string{"投诉", "差评", "退款失败"},
		},
		Priority: PrioritySet{
			P0Keywords: []string{"系统宕机", "无法登录", "支付失败", "数据丢失", "全站挂掉"},
			P1Keywords: []string{"退款失败", "登录异常", "崩溃", "卡顿", "订单错误"},
			P2Keywords: []string{"闪退", "报错", "无法使用"},
			P3Keywords: []string{"建议", "咨询", "反馈"},
		},
	}
	return map[string]Template{"general_v1": general}
}

// renderIntentTemplate / renderSensitiveTemplate / renderPriorityTemplate
// turn the structured template into the Jev instructions body.
func renderIntentTemplate(t *Template) string {
	return fmt.Sprintf(`判断用户消息的咨询意图，从 label 中选择最匹配的一个。
label 列表：%s

用户消息：{{.message}}
{{if .history}}历史对话：{{.history}}{{end}}

输出 JSON：{"label": "...", "score": 0.0-1.0}`, strings.Join(t.Intent.Labels, ", "))
}

func renderSensitiveTemplate(t *Template) string {
	v := strings.Join(t.Sensitive.ViolateWords, "、")
	s := strings.Join(t.Sensitive.SensitiveWords, "、")
	return fmt.Sprintf(`判断下列内容是否包含敏感或违规信息。
违规词：%s
敏感词：%s

待审内容：{{.content}}

输出 JSON：{"label": "clean|sensitive|violate", "score": 0.0-1.0}`, v, s)
}

func renderPriorityTemplate(t *Template) string {
	return fmt.Sprintf(`判断工单的紧急度。
p0 关键词：%s
p1 关键词：%s
p2 关键词：%s
p3 关键词：%s

工单描述：{{.description}}
客户等级：{{.customer_tier}}

输出 JSON：{"label": "p0|p1|p2|p3", "score": 0.0-1.0}`,
		strings.Join(t.Priority.P0Keywords, "、"),
		strings.Join(t.Priority.P1Keywords, "、"),
		strings.Join(t.Priority.P2Keywords, "、"),
		strings.Join(t.Priority.P3Keywords, "、"))
}

func sensitiveFallback(t *Template) jev.FallbackSpec {
	if t.Sensitive.Conservative {
		return jev.FallbackSpec{Type: "conservative", Value: "sensitive"}
	}
	return jev.FallbackSpec{Type: "default", Value: "clean"}
}

// timeNow is a thin indirection so tests can stub it.
var timeNow = func() time.Time { return time.Now() }
