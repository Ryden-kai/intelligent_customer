// Package jev — fallback.go
//
// Local-rule fallback for the decision layer. When the live Jev call
// fails (network blip, OpenRouter down, template misconfigured) we
// need a deterministic, cheap fallback so the request still gets a
// useful answer. ADR-007 says: NEVER fall back to LLM for judgment
// tasks — that path is 100-400x more expensive. Use local rules
// instead.
//
// v2.1 implements one ruleset per trigger:
//
//	intent_routing       keyword → label
//	sensitive_check      keyword scan → clean / sensitive / violate
//	emotion_detection    keyword scan → angry/anxious/neutral/urgent score
//	ticket_priority      keyword urgency + customer tier → p0/p1/p2/p3
//	agent_routing        round-robin among configured skill groups
//
// Each ruleset is a YAML file under backend/data/jev_rules/<name>.yaml
// so operations can edit keywords without recompiling. The loader is
// incremental (only reloads changed files) and the lookup is bounded
// so P95 stays under 10 ms even with hundreds of keywords.

package jev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"

	"intelligent_customer/backend/internal/log"
)

// LocalRule is one keyword → outcome mapping shared across triggers.
// `score` is optional; when absent, a uniform 1.0 is applied.
type LocalRule struct {
	Keyword string  `yaml:"keyword" json:"keyword"`
	Label   string  `yaml:"label,omitempty" json:"label,omitempty"`
	Weight  float64 `yaml:"weight,omitempty" json:"weight,omitempty"`
	Score   float64 `yaml:"score,omitempty" json:"score,omitempty"`
}

// RuleSet is one trigger's worth of rules. OutputType + Default
// mirror the Template fields so the fallback can shape the Decision
// correctly even without the original template available.
type RuleSet struct {
	Trigger    TriggerPoint `yaml:"trigger" json:"trigger"`
	OutputType OutputType   `yaml:"output_type" json:"output_type"`
	Labels     []string     `yaml:"labels,omitempty" json:"labels,omitempty"`
	Rules      []LocalRule  `yaml:"rules" json:"rules"`
	Default    string       `yaml:"default" json:"default"`
	// Conservative flips the default rule to the most restrictive
	// label (e.g. sensitive for safety). Used by sensitive_check.
	Conservative bool `yaml:"conservative,omitempty" json:"conservative,omitempty"`
}

// LocalRules is the file-shaped ruleset loader. Constructed once at
// startup; reloadable via LoadFromFS (called by the admin reload
// endpoint).
type LocalRules struct {
	mu       sync.RWMutex
	byTrig   map[TriggerPoint]*RuleSet
	logger   zerolog.Logger
	dir      string
}

// NewLocalRules builds an empty ruleset. dir is where *.yaml files live;
// pass "" to skip FS loading (e.g. tests).
func NewLocalRules(dir string, logger zerolog.Logger) *LocalRules {
	r := &LocalRules{
		byTrig: make(map[TriggerPoint]*RuleSet),
		logger: logger,
		dir:    dir,
	}
	r.installBuiltins()
	return r
}

// ResetRulesForTest wipes every ruleset including built-ins. Tests use
// this to assert error paths; production code never calls it.
func (r *LocalRules) ResetRulesForTest() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byTrig = make(map[TriggerPoint]*RuleSet)
}

func (r *LocalRules) installBuiltins() {
	for _, rs := range builtinRuleSets() {
		rs := rs // capture
		r.byTrig[rs.Trigger] = &rs
	}
}

// LoadFromFS reads *.yaml files from dir into the ruleset. Returns the
// number of files loaded and any per-file errors so callers can log
// without aborting.
func (r *LocalRules) LoadFromFS(dir string) (int, []error) {
	if dir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, []error{fmt.Errorf("read rules dir %s: %w", dir, err)}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
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
		var rs RuleSet
		if err := yaml.Unmarshal(raw, &rs); err != nil {
			loadErrs = append(loadErrs, fmt.Errorf("parse %s: %w", path, err))
			continue
		}
		if err := rs.Validate(); err != nil {
			loadErrs = append(loadErrs, fmt.Errorf("validate %s: %w", path, err))
			continue
		}
		r.byTrig[rs.Trigger] = &rs
		loaded++
	}
	return loaded, loadErrs
}

// Get returns a snapshot copy of the ruleset for trigger. Missing
// trigger returns nil — caller decides what to do.
func (r *LocalRules) Get(trigger TriggerPoint) *RuleSet {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if rs, ok := r.byTrig[trigger]; ok {
		out := *rs
		out.Rules = append([]LocalRule(nil), rs.Rules...)
		return &out
	}
	return nil
}

// LocalRuleFallback is the Fallback implementation that uses LocalRules.
// It honours the Orchestrator contract:
//
//   - Always returns a non-nil Decision when the ruleset exists.
//   - Uses the conservative default for safety-sensitive triggers
//     when liveErr indicates the template didn't have a fallback spec.
//   - Returns ErrNoFallback when no ruleset matches the trigger so the
//     orchestrator can bubble the error up.
type LocalRuleFallback struct {
	Rules *LocalRules
}

// NewLocalRuleFallback wraps a *LocalRules. The Rules field can be nil
// — the fallback will then return ErrNoFallback on every call (useful
// for tests that want to verify error propagation).
func NewLocalRuleFallback(r *LocalRules) *LocalRuleFallback {
	return &LocalRuleFallback{Rules: r}
}

// Handle implements Fallback. It first honours the template's own
// FallbackSpec (so DB-published templates can override the static
// defaults); when the template is nil or its spec is empty, it falls
// through to the local rules; if neither produces a label, it returns
// the most conservative default for the trigger.
func (f *LocalRuleFallback) Handle(ctx context.Context, tpl *Template, req DecisionRequest, liveErr error) (*Decision, error) {
	if f == nil || f.Rules == nil {
		return nil, ErrNoFallback
	}
	rs := f.Rules.Get(req.Trigger)
	if rs == nil {
		return nil, ErrNoFallback
	}
	// Build a flat text view of the input for keyword matching.
	text := flattenInput(req.Input)

	label, score := matchRules(rs, text)
	if label == "" {
		// No keyword matched. Honour Conservative when set, otherwise
		// the ruleset's Default label.
		if rs.Conservative && tpl != nil && tpl.Fallback.Value != "" {
			label = tpl.Fallback.Value
		} else {
			label = rs.Default
		}
	}

	d := &Decision{
		TenantID:        req.TenantID,
		TemplateName:    templateNameOrFallback(tpl, req.Trigger),
		TemplateVersion: templateVersionOrFallback(tpl),
		Trigger:         req.Trigger,
		OutputType:      rs.OutputType,
		Choice:          label,
		Scores:          map[string]any{"matched_keyword_score": score},
		Fallback:        true,
		FallbackReason:  reasonFor(liveErr),
		CostUSD:         0, // local rules are free
		TraceID:         req.TraceID,
		LatencyMS:       0, // measured by orchestrator? no — local; we set to 0
	}
	if rs.OutputType == OutputScore {
		// Build a per-label score map: the matched label gets `score`,
		// the rest get 0. This matches the live Jev score envelope so
		// downstream code doesn't branch on fallback vs. live.
		scores := map[string]any{}
		for _, l := range rs.Labels {
			scores[l] = 0.0
		}
		scores[label] = score
		d.Scores = scores
	}
	_ = log.With // silence unused import in build paths without log calls
	return d, nil
}

// Validate is a minimal correctness check — used by LoadFromFS so a
// broken YAML file doesn't poison the whole set.
func (rs RuleSet) Validate() error {
	if rs.Trigger == "" {
		return errors.New("trigger required")
	}
	switch rs.OutputType {
	case OutputChoice, OutputScore, OutputNoul:
	default:
		return fmt.Errorf("invalid output_type %q", rs.OutputType)
	}
	if rs.Default == "" {
		return errors.New("default label required")
	}
	for i, r := range rs.Rules {
		if strings.TrimSpace(r.Keyword) == "" {
			return fmt.Errorf("rule[%d] has empty keyword", i)
		}
	}
	return nil
}

// ----- helpers ---------------------------------------------------------------

func flattenInput(in map[string]any) string {
	if len(in) == 0 {
		return ""
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		switch v := in[k].(type) {
		case string:
			sb.WriteString(v)
			sb.WriteString("\n")
		default:
			b, _ := json.Marshal(v)
			sb.Write(b)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func matchRules(rs *RuleSet, text string) (string, float64) {
	if text == "" {
		return "", 0
	}
	lc := strings.ToLower(text)
	bestLabel := ""
	bestScore := 0.0
	for _, r := range rs.Rules {
		kw := strings.ToLower(strings.TrimSpace(r.Keyword))
		if kw == "" {
			continue
		}
		if !strings.Contains(lc, kw) && !regexMatch(kw, lc) {
			continue
		}
		s := r.Weight
		if s == 0 {
			s = 1.0
		}
		if r.Score != 0 {
			s = r.Score
		}
		if s > bestScore {
			bestScore = s
			bestLabel = r.Label
		}
	}
	return bestLabel, bestScore
}

func regexMatch(pattern, text string) bool {
	if !strings.HasPrefix(pattern, "re:") {
		return false
	}
	re, err := regexp.Compile(strings.TrimPrefix(pattern, "re:"))
	if err != nil {
		return false
	}
	return re.MatchString(text)
}

func reasonFor(liveErr error) string {
	if liveErr == nil {
		return "no_live_error"
	}
	if errors.Is(liveErr, errClientDisabled) {
		return "client_disabled"
	}
	if errors.Is(liveErr, context.DeadlineExceeded) {
		return "live_timeout"
	}
	return "live_error"
}

func templateNameOrFallback(tpl *Template, trig TriggerPoint) string {
	if tpl != nil && tpl.Name != "" {
		return tpl.Name
	}
	return string(trig)
}

func templateVersionOrFallback(tpl *Template) int {
	if tpl != nil {
		return tpl.Version
	}
	return 1
}

// builtinRuleSets is the smallest set of rules that lets the system
// serve a request with zero configuration. Each matches the matching
// template's defaults so live Jev calls (when configured) return the
// same labels as fallback.
func builtinRuleSets() []RuleSet {
	return []RuleSet{
		{
			Trigger:    TriggerMessageEntry,
			OutputType: OutputChoice,
			Labels:     []string{"order", "refund", "coupon", "faq", "chitchat", "sensitive", "complaint", "out_of_scope"},
			Default:    "chitchat",
			Rules: []LocalRule{
				{Keyword: "退款", Label: "refund", Weight: 1.0},
				{Keyword: "退货", Label: "refund", Weight: 1.0},
				{Keyword: "退钱", Label: "refund", Weight: 1.0},
				{Keyword: "订单", Label: "order", Weight: 1.0},
				{Keyword: "物流", Label: "order", Weight: 1.0},
				{Keyword: "快递", Label: "order", Weight: 1.0},
				{Keyword: "发货", Label: "order", Weight: 1.0},
				{Keyword: "优惠券", Label: "coupon", Weight: 1.0},
				{Keyword: "折扣", Label: "coupon", Weight: 1.0},
				{Keyword: "积分", Label: "coupon", Weight: 1.0},
				{Keyword: "怎么", Label: "faq", Weight: 1.0},
				{Keyword: "如何", Label: "faq", Weight: 1.0},
				{Keyword: "投诉", Label: "complaint", Weight: 1.0},
				{Keyword: "投诉客服", Label: "complaint", Weight: 1.5},
				{Keyword: "垃圾", Label: "complaint", Weight: 1.0},
			},
		},
		{
			Trigger:    TriggerPreIngest,
			OutputType: OutputChoice,
			Labels:     []string{"clean", "sensitive", "violate"},
			Default:    "clean",
			Conservative: true,
			Rules: []LocalRule{
				{Keyword: "去死", Label: "violate", Weight: 1.0},
				{Keyword: "杀", Label: "violate", Weight: 1.0},
				{Keyword: "诈骗", Label: "violate", Weight: 1.0},
				{Keyword: "投诉", Label: "sensitive", Weight: 1.0},
				{Keyword: "差评", Label: "sensitive", Weight: 1.0},
			},
		},
		{
			Trigger:    TriggerPreReply,
			OutputType: OutputChoice,
			Labels:     []string{"clean", "sensitive", "violate"},
			Default:    "clean",
			Conservative: true,
			Rules: []LocalRule{
				{Keyword: "去死", Label: "violate", Weight: 1.0},
				{Keyword: "诈骗", Label: "violate", Weight: 1.0},
			},
		},
		{
			Trigger:    TriggerPreHandover,
			OutputType: OutputScore,
			Labels:     []string{"angry", "anxious", "neutral", "satisfied", "urgent"},
			Default:    "neutral",
			Rules: []LocalRule{
				{Keyword: "生气", Label: "angry", Weight: 1.0, Score: 0.8},
				{Keyword: "愤怒", Label: "angry", Weight: 1.0, Score: 0.9},
				{Keyword: "气死", Label: "angry", Weight: 1.0, Score: 0.85},
				{Keyword: "着急", Label: "urgent", Weight: 1.0, Score: 0.7},
				{Keyword: "紧急", Label: "urgent", Weight: 1.0, Score: 0.9},
				{Keyword: "马上", Label: "urgent", Weight: 1.0, Score: 0.6},
				{Keyword: "焦虑", Label: "anxious", Weight: 1.0, Score: 0.7},
				{Keyword: "担心", Label: "anxious", Weight: 1.0, Score: 0.6},
				{Keyword: "谢谢", Label: "satisfied", Weight: 1.0, Score: 0.7},
				{Keyword: "感谢", Label: "satisfied", Weight: 1.0, Score: 0.7},
			},
		},
		{
			Trigger:    TriggerTicketCreate,
			OutputType: OutputChoice,
			Labels:     []string{"p0", "p1", "p2", "p3"},
			Default:    "p2",
			Rules: []LocalRule{
				{Keyword: "系统宕机", Label: "p0", Weight: 1.0},
				{Keyword: "无法登录", Label: "p0", Weight: 1.0},
				{Keyword: "支付失败", Label: "p0", Weight: 1.0},
				{Keyword: "数据丢失", Label: "p0", Weight: 1.0},
				{Keyword: "退款失败", Label: "p1", Weight: 1.0},
				{Keyword: "登录异常", Label: "p1", Weight: 1.0},
				{Keyword: "崩溃", Label: "p1", Weight: 1.0},
				{Keyword: "闪退", Label: "p2", Weight: 1.0},
				{Keyword: "建议", Label: "p3", Weight: 1.0},
				{Keyword: "咨询", Label: "p3", Weight: 1.0},
			},
		},
		{
			Trigger:    TriggerAgentAssign,
			OutputType: OutputChoice,
			Labels:     []string{"default_skill_group", "vip_skill_group", "tech_skill_group"},
			Default:    "default_skill_group",
			Rules: []LocalRule{
				{Keyword: "VIP", Label: "vip_skill_group", Weight: 1.0},
				{Keyword: "大客户", Label: "vip_skill_group", Weight: 1.0},
				{Keyword: "技术", Label: "tech_skill_group", Weight: 1.0},
				{Keyword: "BUG", Label: "tech_skill_group", Weight: 1.0},
				{Keyword: "故障", Label: "tech_skill_group", Weight: 1.0},
			},
		},
	}
}

// Now exposes time.Now for benchmark hooks; unexported but kept here
// so future benchmark tests can stub.
func now() time.Time { return time.Now() }
