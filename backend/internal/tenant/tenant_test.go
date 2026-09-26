package tenant

import (
	"context"
	"testing"

	"intelligent_customer/backend/internal/testutil"
)

func TestFromContext_DefaultWhenMissing(t *testing.T) {
	got := FromContext(context.Background())
	if got.ID != DefaultID {
		t.Fatalf("expected default tenant id %q, got %q", DefaultID, got.ID)
	}
	if !got.IsDefault {
		t.Fatal("expected IsDefault=true")
	}
}

func TestWithTenant_OverridesDefault(t *testing.T) {
	info := Info{ID: "tnt_acme", Name: "Acme", Region: "intl"}
	ctx := WithTenant(context.Background(), info)
	got := FromContext(ctx)
	if got.ID != "tnt_acme" || got.Name != "Acme" || got.Region != "intl" {
		t.Fatalf("unexpected info: %+v", got)
	}
}

func TestWithTenant_EmptyIsNoop(t *testing.T) {
	ctx := WithTenant(context.Background(), Info{}) // zero value
	if got := FromContext(ctx); got.ID != DefaultID {
		t.Fatalf("zero-value Info must not stick; got %q", got.ID)
	}
}

func TestMustFromContext_Missing(t *testing.T) {
	_, err := MustFromContext(context.Background())
	if !IsMissing(err) {
		t.Fatalf("expected IsMissing=true, got %v", err)
	}
}

func TestMustFromContext_Present(t *testing.T) {
	ctx := WithTenant(context.Background(), Info{ID: "tnt_x"})
	got, err := MustFromContext(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.ID != "tnt_x" {
		t.Fatalf("got %q", got.ID)
	}
}

func TestEqual(t *testing.T) {
	a := Info{ID: "tnt_a"}
	b := Info{ID: "tnt_a"}
	c := Info{ID: "tnt_c"}
	if !a.Equal(b) {
		t.Fatal("expected a.Equal(b)")
	}
	if a.Equal(c) {
		t.Fatal("expected a != c")
	}
	if (Info{}).Equal(a) {
		t.Fatal("zero-value should never equal a populated tenant")
	}
}

func TestFromHeader_Validates(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"tnt_default", "tnt_default"},
		{"  tnt_acme  ", "tnt_acme"},
		{"tnt-with-dash", "tnt-with-dash"},
		{"tnt_with_under", "tnt_with_under"},
		{"bad id with space", ""},
		{"bad/slash", ""},
		{"bad;DROP", ""},
	}
	for _, tc := range cases {
		if got := FromHeader(tc.in); got != tc.want {
			t.Errorf("FromHeader(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Oversize id rejected
	big := make([]byte, 65)
	for i := range big {
		big[i] = 'a'
	}
	if got := FromHeader(string(big)); got != "" {
		t.Errorf("expected oversize id to be rejected, got %q", got)
	}
}

func TestRepo_EnsureDefaultIsIdempotent(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewRepo(conn)
	ctx := context.Background()

	first, err := repo.EnsureDefault(ctx)
	if err != nil {
		t.Fatalf("EnsureDefault first call: %v", err)
	}
	if first.ID != DefaultID {
		t.Fatalf("expected default id, got %q", first.ID)
	}
	second, err := repo.EnsureDefault(ctx)
	if err != nil {
		t.Fatalf("EnsureDefault second call: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("id drift across EnsureDefault calls")
	}
}

func TestRepo_UpsertAndGet(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewRepo(conn)
	ctx := context.Background()

	tnt := &Tenant{
		ID:           "tnt_acme",
		Name:         "Acme",
		Region:       "intl",
		Status:       "active",
		LLMPrimary:   "openai",
		LLMSecondary: "openrouter",
		LLMTertiary:  "minimax",
		ConfigJSON:   `{"branding":"acme"}`,
	}
	if err := repo.Upsert(ctx, tnt); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := repo.Get(ctx, "tnt_acme")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Acme" || got.Region != "intl" || got.LLMPrimary != "openai" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.ToInfo().IsDefault {
		t.Fatal("tnt_acme must not be IsDefault")
	}
}

func TestRepo_DeleteDefaultRefused(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewRepo(conn)
	if _, err := repo.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := repo.Delete(context.Background(), DefaultID); err == nil {
		t.Fatal("expected error when deleting tnt_default")
	}
}

func TestRepo_DeleteUnknownReturnsErrNotFound(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewRepo(conn)
	if _, err := repo.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := repo.Delete(context.Background(), "tnt_ghost")
	if err == nil {
		t.Fatal("expected error for unknown tenant")
	}
}

func TestRepo_List(t *testing.T) {
	conn, _ := testutil.OpenTempSQLite(t)
	repo := NewRepo(conn)
	ctx := context.Background()
	if _, err := repo.EnsureDefault(ctx); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	if err := repo.Upsert(ctx, &Tenant{ID: "tnt_b", Name: "B", Region: "cn"}); err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	if err := repo.Upsert(ctx, &Tenant{ID: "tnt_a", Name: "A", Region: "intl"}); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	out, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 tenants, got %d", len(out))
	}
	// Ordered by id ASC (SQLite byte order): '_' (95) < 'a' (97) < 'b' (98) < 'd' (100),
	// so tnt_a < tnt_b < tnt_default.
	if out[0].ID != "tnt_a" || out[1].ID != "tnt_b" || out[2].ID != DefaultID {
		t.Fatalf("unexpected ordering: %v", out)
	}
}
