package repo_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"intelligent_customer/backend/internal/model"
	"intelligent_customer/backend/internal/repo"
	"intelligent_customer/backend/internal/testutil"
)

func TestConversationCRUD(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()

	convs := repo.NewConversations(conn)
	ctx := context.Background()
	c, err := convs.Create(ctx, "u1", "测试标题")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.ID == "" || c.Status != model.ConvStatusOpen {
		t.Fatalf("bad conv: %+v", c)
	}
	if err := convs.SetHandedOver(ctx, c.ID); err != nil {
		t.Fatalf("SetHandedOver: %v", err)
	}
	got, err := convs.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.HandedOver || got.Status != model.ConvStatusHandedOver {
		t.Fatalf("status not updated: %+v", got)
	}

	if err := convs.Close(ctx, c.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got2, _ := convs.Get(ctx, c.ID)
	if got2.Status != model.ConvStatusClosed {
		t.Fatalf("close failed: %+v", got2)
	}
}

func TestMessagesRoundTrip(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	convs := repo.NewConversations(conn)
	msgs := repo.NewMessages(conn)
	ctx := context.Background()

	c, err := convs.Create(ctx, "u1", "title")
	if err != nil {
		t.Fatal(err)
	}

	for i, body := range []string{"hi", "你好", "再见"} {
		m := &model.Message{ConversationID: c.ID, Role: model.RoleUser, Content: body}
		if err := msgs.Insert(ctx, m); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	got, err := msgs.ListByConversation(ctx, c.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	if got[0].Content != "hi" || got[2].Content != "再见" {
		t.Fatalf("order broken: %+v", got)
	}

	// LastN returns in chronological order (reverse of the DESC scan).
	last, err := msgs.LastN(ctx, c.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(last) != 2 || last[0].Content != "你好" {
		t.Fatalf("LastN: %+v", last)
	}
}

func TestFAQsListAndSeed(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	faqs := repo.NewFAQs(conn)
	ctx := context.Background()

	// Empty DB returns nothing.
	got, err := faqs.ListEnabled(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d", len(got))
	}
}

func TestFeedbackUpsert(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	convs := repo.NewConversations(conn)
	fb := repo.NewFeedback(conn)
	ctx := context.Background()

	c, _ := convs.Create(ctx, "u", "x")

	// First insert
	if err := fb.Upsert(ctx, &model.Feedback{ConversationID: c.ID, Rating: 5, Comment: "好"}); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	// Second insert should replace
	if err := fb.Upsert(ctx, &model.Feedback{ConversationID: c.ID, Rating: 2, Comment: "差"}); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
}

func TestHandoverSignalCount(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	convs := repo.NewConversations(conn)
	sig := repo.NewHandoverSignals(conn)
	ctx := context.Background()
	c, _ := convs.Create(ctx, "u", "x")

	for i := 0; i < 3; i++ {
		if err := sig.Insert(ctx, &model.HandoverSignal{ConversationID: c.ID, Source: "test"}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	n, err := sig.CountByConv(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("count: %d", n)
	}
}

// quiet unused import warning
var _ = strings.Contains

func TestAdminUsersCRUD(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	admins := repo.NewAdminUsers(conn)
	ctx := context.Background()

	// Empty list.
	all, err := admins.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("expected empty, got %d", len(all))
	}
	if n, _ := admins.Count(ctx); n != 0 {
		t.Fatalf("expected count 0, got %d", n)
	}

	// Insert.
	hashA := "$argon2id$v=19$m=1,t=1,p=1$AAAA$BBBB"
	hashB := "$argon2id$v=19$m=1,t=1,p=1$CCCC$DDDD"
	a, err := admins.Insert(ctx, "alice", hashA, "admin")
	if err != nil {
		t.Fatalf("insert alice: %v", err)
	}
	if a.ID == "" || a.Username != "alice" || a.Role != "admin" {
		t.Fatalf("bad row: %+v", a)
	}
	if _, err := admins.Insert(ctx, "bob", hashB, "admin"); err != nil {
		t.Fatalf("insert bob: %v", err)
	}

	// Get by username and id both work.
	got, err := admins.GetByUsername(ctx, "alice")
	if err != nil || got.ID != a.ID {
		t.Fatalf("GetByUsername alice: %v %+v", err, got)
	}
	got, err = admins.GetByID(ctx, a.ID)
	if err != nil || got.Username != "alice" {
		t.Fatalf("GetByID: %v %+v", err, got)
	}

	// LookupByUsernameOrID accepts either form.
	if _, err := admins.LookupByUsernameOrID(ctx, "alice"); err != nil {
		t.Fatalf("lookup by username: %v", err)
	}
	if _, err := admins.LookupByUsernameOrID(ctx, a.ID); err != nil {
		t.Fatalf("lookup by id: %v", err)
	}

	// Username collision returns the underlying UNIQUE-constraint error;
	// the handler decides whether to map it to 409.
	if _, err := admins.Insert(ctx, "alice", hashA, "admin"); err == nil {
		t.Fatal("expected UNIQUE violation on duplicate username")
	}

	// Update password / username.
	if err := admins.UpdatePassword(ctx, a.ID, hashB); err != nil {
		t.Fatalf("update password: %v", err)
	}
	if got, _ := admins.GetByID(ctx, a.ID); got.PasswordHash != hashB {
		t.Fatalf("password not updated: %+v", got)
	}
	if err := admins.UpdateUsername(ctx, a.ID, "alice2"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got, _ := admins.GetByID(ctx, a.ID); got.Username != "alice2" {
		t.Fatalf("rename not applied: %+v", got)
	}

	// TouchLastLogin + List returns both rows ordered by username.
	if err := admins.TouchLastLogin(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	all, _ = admins.List(ctx)
	if len(all) != 2 || all[0].Username != "alice2" {
		t.Fatalf("list: %+v", all)
	}
	if all[0].LastLoginAt == nil {
		t.Fatal("expected last_login_at to be set on alice2 (a.ID)")
	}

	// ErrNotFound on missing user.
	if _, err := admins.GetByUsername(ctx, "nobody"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	// Delete the second account (alice2), then re-create alice2 to verify
	// deletion doesn't poison the UNIQUE index.
	if err := admins.Delete(ctx, a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := admins.Insert(ctx, "alice2", hashA, "admin"); err != nil {
		t.Fatalf("re-insert alice2 after delete: %v", err)
	}
}