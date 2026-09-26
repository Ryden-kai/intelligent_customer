package industry

import (
	"context"
	"errors"
	"testing"

	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/tenant"
	"intelligent_customer/backend/internal/testutil"
)

func TestBuiltinTemplates_ContainsGeneralV1(t *testing.T) {
	l := NewLoader("", nil)
	tpl, err := l.Load(context.Background(), "general_v1")
	if err != nil {
		t.Fatalf("Load general_v1: %v", err)
	}
	if tpl.Code != "general_v1" || len(tpl.Intent.Labels) < 5 {
		t.Fatalf("unexpected general_v1 shape: %+v", tpl)
	}
}

func TestLoad_UnknownCodeReturnsErr(t *testing.T) {
	l := NewLoader("", nil)
	_, err := l.Load(context.Background(), "ghost_v1")
	if !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("expected ErrTemplateNotFound, got %v", err)
	}
}

func TestLoadFromFS_OverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	yaml := `
code: general_v1
name: Custom General
version: 2
description: custom
intent:
  labels: [a, b]
  fallback_label: a
sensitive:
  violate_words: []
  sensitive_words: []
priority:
  p0_keywords: []
  p1_keywords: []
`
	if err := writeFile(dir+"/general_v1.yaml", []byte(yaml)); err != nil {
		t.Fatal(err)
	}
	l := NewLoader("", nil)
	loaded, errs := l.LoadFromFS(dir)
	if loaded != 1 || len(errs) != 0 {
		t.Fatalf("expected 1 file loaded no errors, got %d/%v", loaded, errs)
	}
	tpl, err := l.Load(context.Background(), "general_v1")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Name != "Custom General" || tpl.Version != 2 {
		t.Fatalf("FS override failed: %+v", tpl)
	}
	if len(tpl.Intent.Labels) != 2 || tpl.Intent.Labels[0] != "a" {
		t.Fatalf("intent labels not overridden: %+v", tpl.Intent)
	}
}

func TestLoadFromFS_BadFileSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir+"/bad.yaml", []byte("code: \nversion: not_int")); err != nil {
		t.Fatal(err)
	}
	l := NewLoader("", nil)
	loaded, errs := l.LoadFromFS(dir)
	if loaded != 0 || len(errs) != 1 {
		t.Fatalf("expected 0 loaded + 1 err, got %d/%v", loaded, errs)
	}
}

func TestLoadFromFS_MissingDirIsSilent(t *testing.T) {
	l := NewLoader("", nil)
	loaded, errs := l.LoadFromFS("/definitely/not/here")
	if loaded != 0 || len(errs) != 0 {
		t.Fatalf("missing dir should be silent, got %d/%v", loaded, errs)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		tpl  Template
		ok   bool
	}{
		{
			name: "valid",
			tpl: Template{
				Code: "x", Name: "x", Version: 1,
				Intent: IntentSet{Labels: []string{"a"}, FallbackLabel: "a"},
			},
			ok: true,
		},
		{
			name: "no code",
			tpl: Template{
				Name: "x",
				Intent: IntentSet{Labels: []string{"a"}, FallbackLabel: "a"},
			},
			ok: false,
		},
		{
			name: "no labels",
			tpl: Template{Code: "x", Name: "x"},
			ok: false,
		},
		{
			name: "no fallback label",
			tpl: Template{Code: "x", Name: "x", Intent: IntentSet{Labels: []string{"a"}}},
			ok: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.tpl.Validate()
			if tc.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestActivate_MaterialisesThreeRows(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := jev.NewTemplateRepo(conn)
	l := NewLoader("", nil)
	ctx := context.Background()

	n, err := l.Activate(ctx, tenant.DefaultID, "general_v1", repo)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 rows (intent+sensitive+priority), got %d", n)
	}
	rows, _ := repo.ListByTenant(ctx, tenant.DefaultID)
	if len(rows) != 3 {
		t.Fatalf("expected 3 templates, got %d", len(rows))
	}
	names := map[string]bool{}
	for _, r := range rows {
		names[r.Name] = true
		if r.Status != "published" {
			t.Fatalf("expected status=published, got %s for %s", r.Status, r.Name)
		}
		if r.IndustryCode != "general_v1" {
			t.Fatalf("expected industry_code=general_v1, got %s", r.IndustryCode)
		}
	}
	for _, want := range []string{"intent_routing", "sensitive_check", "ticket_priority"} {
		if !names[want] {
			t.Fatalf("missing materialised template %s", want)
		}
	}
}

func TestActivate_ReActivateBumpsVersion(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := jev.NewTemplateRepo(conn)
	l := NewLoader("", nil)
	ctx := context.Background()

	if _, err := l.Activate(ctx, tenant.DefaultID, "general_v1", repo); err != nil {
		t.Fatal(err)
	}
	// Re-activate: should bump versions on the existing rows
	// (3 v1 rows + 3 v2 rows = 6 total). The published v2 row
	// supersedes v1 from the orchestrator's perspective (registry
	// dedupe keeps highest version).
	if _, err := l.Activate(ctx, tenant.DefaultID, "general_v1", repo); err != nil {
		t.Fatal(err)
	}
	rows, _ := repo.ListByTenant(ctx, tenant.DefaultID)
	if len(rows) != 6 {
		t.Fatalf("expected 6 rows (v1 + v2), got %d", len(rows))
	}
	v2Seen := 0
	for _, r := range rows {
		if r.Version == 2 {
			v2Seen++
		}
	}
	if v2Seen != 3 {
		t.Fatalf("expected 3 v2 rows, got %d", v2Seen)
	}
}

func TestActivate_NilRepoErrors(t *testing.T) {
	l := NewLoader("", nil)
	_, err := l.Activate(context.Background(), tenant.DefaultID, "general_v1", nil)
	if err == nil {
		t.Fatal("expected error when repo is nil")
	}
}

func TestActivate_UnknownCodeErrors(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := jev.NewTemplateRepo(conn)
	l := NewLoader("", nil)
	_, err := l.Activate(context.Background(), tenant.DefaultID, "ghost_v1", repo)
	if !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("expected ErrTemplateNotFound, got %v", err)
	}
}

func writeFile(path string, body []byte) error {
	return osWriteFile(path, body)
}
