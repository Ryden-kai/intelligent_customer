package jev

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"intelligent_customer/backend/internal/tenant"
	"intelligent_customer/backend/internal/testutil"
)

func TestNewRegistry_HasBuiltins(t *testing.T) {
	r := NewRegistry()
	for _, trig := range []TriggerPoint{
		TriggerMessageEntry, TriggerPreIngest, TriggerPreHandover,
		TriggerTicketCreate, TriggerAgentAssign,
	} {
		if _, err := r.Get(tenant.DefaultID, trig); err != nil {
			t.Fatalf("missing builtin for trigger=%s: %v", trig, err)
		}
	}
}

func TestRegistry_GetFallbacksToDefaultTenant(t *testing.T) {
	r := NewRegistry()
	// Tenant that has nothing of its own.
	got, err := r.Get("tnt_unknown", TriggerMessageEntry)
	if err != nil {
		t.Fatalf("expected fallback to default tenant, got err: %v", err)
	}
	if got.Name != "intent_routing" {
		t.Fatalf("expected intent_routing builtin, got %s", got.Name)
	}
}

func TestRegistry_GetUnknownTriggerReturnsNotFound(t *testing.T) {
	r := NewRegistry()
	r.Reset() // wipe builtins
	_, err := r.Get(tenant.DefaultID, TriggerMessageEntry)
	if err == nil || !strings.Contains(err.Error(), "no jev template") {
		t.Fatalf("expected not-found, got %v", err)
	}
}

func TestRegistry_LoadFromFS(t *testing.T) {
	dir := t.TempDir()
	yaml := `
name: intent_routing
version: 7
description: custom
trigger: message.entry
output_type: choice
labels: [order, refund, faq]
template: |
  custom instructions: {{.message}}
fallback:
  type: default
  value: faq
`
	if err := writeFile(t, filepath.Join(dir, "intent_routing.yaml"), yaml); err != nil {
		t.Fatal(err)
	}
	// Invalid file must not poison the whole load.
	if err := writeFile(t, filepath.Join(dir, "bad.yaml"), "this: is: not: valid: yaml: : :"); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	loaded, errs := r.LoadFromFS(dir)
	if loaded != 1 {
		t.Fatalf("expected 1 template loaded, got %d", loaded)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error from bad.yaml, got %d: %v", len(errs), errs)
	}
	got, err := r.Get(tenant.DefaultID, TriggerMessageEntry)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Version != 7 {
		t.Fatalf("expected FS template version=7, got %d", got.Version)
	}
	if got.Instructions == "" {
		t.Fatal("instructions should round-trip from YAML")
	}
}

func TestRegistry_LoadFromFS_MissingDirIsOK(t *testing.T) {
	r := NewRegistry()
	loaded, errs := r.LoadFromFS("/definitely/not/here")
	if loaded != 0 || len(errs) != 0 {
		t.Fatalf("missing dir must be silent: loaded=%d errs=%v", loaded, errs)
	}
}

func TestRegistry_LoadFromDBOverridesFS(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	ctx := context.Background()

	// Migration 006 already seeds tnt_default; the second insert is a
	// no-op via INSERT OR IGNORE so the test stays deterministic.
	if _, err := conn.ExecContext(ctx,
		`INSERT OR IGNORE INTO tenants(id, name, region, status, created_at, updated_at)
		 VALUES('tnt_default','default','cn','active',1,1)`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	// Insert a published template with higher version.
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO jev_templates(id, tenant_id, name, version, trigger, output_type,
		                           labels_json, instructions, fallback_json, definition_yaml,
		                           status, created_at)
		 VALUES('id-1','tnt_default','intent_routing',99,'message.entry','choice',
		        '["order","refund"]','db instructions','{"type":"default","value":"refund"}',
		        'yaml','published',1)`); err != nil {
		t.Fatalf("insert jev_templates: %v", err)
	}
	r := NewRegistry()
	loaded, err := r.LoadFromDB(ctx, conn)
	if err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	if loaded != 1 {
		t.Fatalf("expected 1 DB row loaded, got %d", loaded)
	}
	got, err := r.Get("tnt_default", TriggerMessageEntry)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Version != 99 || got.Instructions != "db instructions" {
		t.Fatalf("DB did not override builtin: %+v", got)
	}
}

func TestRegistry_ListByTenant(t *testing.T) {
	r := NewRegistry()
	got := r.ListByTenant(tenant.DefaultID)
	if len(got) < 5 {
		t.Fatalf("expected at least 5 builtins, got %d", len(got))
	}
	seen := map[string]bool{}
	for _, tpl := range got {
		if seen[tpl.Name] {
			t.Fatalf("duplicate name in list: %s", tpl.Name)
		}
		seen[tpl.Name] = true
	}
}

func TestRegistry_Validate(t *testing.T) {
	cases := []struct {
		name string
		t    Template
		ok   bool
	}{
		{
			name: "valid choice",
			t: Template{
				Name:         "x",
				Trigger:      TriggerMessageEntry,
				OutputType:   OutputChoice,
				Labels:       []string{"a"},
				Instructions: "pick one",
			},
			ok: true,
		},
		{
			name: "missing name",
			t: Template{
				Trigger:      TriggerMessageEntry,
				OutputType:   OutputChoice,
				Labels:       []string{"a"},
				Instructions: "x",
			},
			ok: false,
		},
		{
			name: "bad output type",
			t: Template{
				Name:         "x",
				Trigger:      TriggerMessageEntry,
				OutputType:   OutputType("wat"),
				Labels:       []string{"a"},
				Instructions: "x",
			},
			ok: false,
		},
		{
			name: "choice without labels",
			t: Template{
				Name:         "x",
				Trigger:      TriggerMessageEntry,
				OutputType:   OutputChoice,
				Instructions: "x",
			},
			ok: false,
		},
		{
			name: "score without labels allowed",
			t: Template{
				Name:         "x",
				Trigger:      TriggerPreHandover,
				OutputType:   OutputScore,
				Instructions: "x",
			},
			ok: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.t.Validate()
			if tc.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestRegistry_ReloadSwapsInMemory(t *testing.T) {
	dir := t.TempDir()
	yaml := `
name: sensitive_check
version: 2
description: custom
trigger: message.pre_ingest
output_type: choice
labels: [clean, sensitive, violate]
template: |
  custom
fallback:
  type: conservative
  value: sensitive
`
	if err := writeFile(t, filepath.Join(dir, "sensitive.yaml"), yaml); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	first := r.LastReloadedAt()
	// Reload: file may not exist yet — verify nothing crashes.
	if _, errs := r.LoadFromFS(dir); len(errs) != 0 {
		t.Fatalf("first reload errs: %v", errs)
	}
	if !r.LastReloadedAt().After(first) {
		t.Fatal("lastReload should advance")
	}
	got, err := r.Get(tenant.DefaultID, TriggerPreIngest)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Version != 2 {
		t.Fatalf("expected version=2 from FS, got %d", got.Version)
	}
}

func writeFile(t *testing.T, path, body string) error {
	t.Helper()
	return writeFileAtomic(path, []byte(body))
}
