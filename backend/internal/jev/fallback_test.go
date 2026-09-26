package jev

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/tenant"
)

func TestLocalRules_BuiltinRulesCoverAllTriggers(t *testing.T) {
	r := NewLocalRules("", zerolog.Nop())
	for _, trig := range AllTriggers {
		if r.Get(trig) == nil {
			t.Fatalf("missing builtin ruleset for %s", trig)
		}
	}
}

func TestLocalRuleFallback_MatchesRefundKeyword(t *testing.T) {
	r := NewLocalRules("", zerolog.Nop())
	fb := NewLocalRuleFallback(r)
	d, err := fb.Handle(context.Background(), nil, DecisionRequest{
		TenantID: tenant.DefaultID,
		Trigger:  TriggerMessageEntry,
		Input:    map[string]any{"message": "我要退款"},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if d.Choice != "refund" {
		t.Fatalf("expected refund, got %s", d.Choice)
	}
	if !d.Fallback || d.CostUSD != 0 {
		t.Fatalf("fallback invariants broken: %+v", d)
	}
}

func TestLocalRuleFallback_NoMatchReturnsDefault(t *testing.T) {
	r := NewLocalRules("", zerolog.Nop())
	fb := NewLocalRuleFallback(r)
	d, err := fb.Handle(context.Background(), nil, DecisionRequest{
		TenantID: tenant.DefaultID,
		Trigger:  TriggerMessageEntry,
		Input:    map[string]any{"message": "随便聊聊"},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if d.Choice != "chitchat" {
		t.Fatalf("expected chitchat default, got %s", d.Choice)
	}
}

func TestLocalRuleFallback_ConservativeFallsBackToSensitiveOnMiss(t *testing.T) {
	r := NewLocalRules("", zerolog.Nop())
	fb := NewLocalRuleFallback(r)
	tpl := &Template{
		Name: "sensitive_check", Version: 1,
		Trigger: TriggerPreIngest, OutputType: OutputChoice,
		Fallback: FallbackSpec{Type: "conservative", Value: "sensitive"},
	}
	d, err := fb.Handle(context.Background(), tpl, DecisionRequest{
		TenantID: tenant.DefaultID,
		Trigger:  TriggerPreIngest,
		Input:    map[string]any{"content": "你们的产品真好用"},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if d.Choice != "sensitive" {
		t.Fatalf("expected sensitive (conservative), got %s", d.Choice)
	}
}

func TestLocalRuleFallback_EmotionScoresAllLabels(t *testing.T) {
	r := NewLocalRules("", zerolog.Nop())
	fb := NewLocalRuleFallback(r)
	d, err := fb.Handle(context.Background(), nil, DecisionRequest{
		TenantID: tenant.DefaultID,
		Trigger:  TriggerPreHandover,
		Input:    map[string]any{"recent_messages": "我非常生气"},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if d.OutputType != OutputScore {
		t.Fatalf("expected score output, got %s", d.OutputType)
	}
	if d.Choice != "angry" {
		t.Fatalf("expected angry, got %s", d.Choice)
	}
	for _, l := range []string{"angry", "anxious", "neutral", "satisfied", "urgent"} {
		if _, ok := d.Scores[l]; !ok {
			t.Fatalf("score map missing label %s: %+v", l, d.Scores)
		}
	}
}

func TestLocalRuleFallback_NoRulesetReturnsErrNoFallback(t *testing.T) {
	r := NewLocalRules("", zerolog.Nop())
	r.ResetRulesForTest() // wipe builtins
	fb := NewLocalRuleFallback(r)
	_, err := fb.Handle(context.Background(), nil, DecisionRequest{
		TenantID: tenant.DefaultID,
		Trigger:  TriggerMessageEntry,
		Input:    map[string]any{"message": "x"},
	}, nil)
	if !errors.Is(err, ErrNoFallback) {
		t.Fatalf("expected ErrNoFallback, got %v", err)
	}
}

func TestLocalRuleFallback_NilRulesReturnsErrNoFallback(t *testing.T) {
	fb := NewLocalRuleFallback(nil)
	_, err := fb.Handle(context.Background(), nil, DecisionRequest{}, nil)
	if err == nil {
		t.Fatal("expected ErrNoFallback when Rules is nil")
	}
}

func TestLocalRules_LoadFromFS(t *testing.T) {
	dir := t.TempDir()
	body := `
trigger: message.entry
output_type: choice
labels: [a, b]
default: a
rules:
  - keyword: foo
    label: a
    weight: 2.0
  - keyword: bar
    label: b
    weight: 1.5
`
	if err := writeFileAtomic(dir+"/custom.yaml", []byte(body)); err != nil {
		t.Fatal(err)
	}
	r := NewLocalRules("", zerolog.Nop())
	loaded, errs := r.LoadFromFS(dir)
	if loaded != 1 || len(errs) != 0 {
		t.Fatalf("expected 1 file loaded no errors, got %d loaded %v errs", loaded, errs)
	}
	rs := r.Get(TriggerMessageEntry)
	if rs == nil {
		t.Fatal("ruleset missing after FS load")
	}
	if len(rs.Rules) != 2 || rs.Rules[0].Keyword != "foo" {
		t.Fatalf("rules not loaded correctly: %+v", rs.Rules)
	}
}

func TestLocalRules_LoadFromFS_BadFileSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := writeFileAtomic(dir+"/broken.yaml", []byte("trigger: \noutput_type: bogus")); err != nil {
		t.Fatal(err)
	}
	r := NewLocalRules("", zerolog.Nop())
	loaded, errs := r.LoadFromFS(dir)
	if loaded != 0 || len(errs) != 1 {
		t.Fatalf("expected 0 loaded + 1 err, got %d/%v", loaded, errs)
	}
}

func TestLocalRules_LoadFromFS_MissingDirIsSilent(t *testing.T) {
	r := NewLocalRules("", zerolog.Nop())
	if loaded, errs := r.LoadFromFS("/definitely/not/here"); loaded != 0 || len(errs) != 0 {
		t.Fatalf("missing dir should be silent, got %d/%v", loaded, errs)
	}
}

func TestRuleSet_Validate(t *testing.T) {
	cases := []struct {
		name string
		rs   RuleSet
		ok   bool
	}{
		{
			name: "valid choice",
			rs: RuleSet{
				Trigger: TriggerMessageEntry, OutputType: OutputChoice,
				Default: "x",
				Rules:   []LocalRule{{Keyword: "k", Label: "x"}},
			},
			ok: true,
		},
		{
			name: "missing default",
			rs: RuleSet{
				Trigger: TriggerMessageEntry, OutputType: OutputChoice,
				Rules: []LocalRule{{Keyword: "k", Label: "x"}},
			},
			ok: false,
		},
		{
			name: "empty keyword",
			rs: RuleSet{
				Trigger: TriggerMessageEntry, OutputType: OutputChoice,
				Default: "x",
				Rules:   []LocalRule{{Keyword: "  ", Label: "x"}},
			},
			ok: false,
		},
		{
			name: "bad output_type",
			rs: RuleSet{
				Trigger: TriggerMessageEntry, OutputType: OutputType("wat"),
				Default: "x",
			},
			ok: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.rs.Validate()
			if tc.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
