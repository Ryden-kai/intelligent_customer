package service_test

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/config"
	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/llm"
	"intelligent_customer/backend/internal/model"
	"intelligent_customer/backend/internal/repo"
	"intelligent_customer/backend/internal/service"
	"intelligent_customer/backend/internal/testutil"
)

func TestAdminListEmpty(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	a := &service.Admin{
		Convs:    repo.NewConversations(conn),
		Msgs:     repo.NewMessages(conn),
		Feedback: repo.NewFeedback(conn),
		Logger:   testutil.QuietLogger(),
	}
	res, err := a.ListConversations(context.Background(), service.ListConversationsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 0 {
		t.Fatalf("total: %d", res.Total)
	}
}

func TestAdminListWithFilterAndPagination(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	convs := repo.NewConversations(conn)
	for i := 0; i < 7; i++ {
		_, _ = convs.Create(context.Background(), "u", "t")
	}
	a := &service.Admin{
		Convs:    convs,
		Msgs:     repo.NewMessages(conn),
		Feedback: repo.NewFeedback(conn),
		Logger:   testutil.QuietLogger(),
	}
	res, err := a.ListConversations(context.Background(), service.ListConversationsParams{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 7 || len(res.Items) != 3 {
		t.Fatalf("got total=%d items=%d", res.Total, len(res.Items))
	}
	if res.Page != 1 || res.PageSize != 3 {
		t.Fatalf("pagination: %+v", res)
	}
}

func TestAdminGetConversationNotFound(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	a := &service.Admin{
		Convs:    repo.NewConversations(conn),
		Msgs:     repo.NewMessages(conn),
		Feedback: repo.NewFeedback(conn),
		Logger:   testutil.QuietLogger(),
	}
	if _, err := a.GetConversation(context.Background(), "nope"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestAdminAppendAgentReply(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	convs := repo.NewConversations(conn)
	msgs := repo.NewMessages(conn)
	c, err := convs.Create(context.Background(), "u", "title")
	if err != nil {
		t.Fatal(err)
	}
	a := &service.Admin{
		Convs:    convs,
		Msgs:     msgs,
		Feedback: repo.NewFeedback(conn),
		Logger:   testutil.QuietLogger(),
	}
	m, err := a.AppendAgentReply(context.Background(), c.ID, "你好我是客服")
	if err != nil {
		t.Fatal(err)
	}
	if m.Role != model.RoleAgent {
		t.Fatalf("role: %q", m.Role)
	}
	if _, err := a.AppendAgentReply(context.Background(), c.ID, "  "); err == nil {
		t.Fatalf("expected error on empty reply")
	}
}

func TestFeedbackSubmit(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	convs := repo.NewConversations(conn)
	fb := repo.NewFeedback(conn)
	msgs := repo.NewMessages(conn)
	c, _ := convs.Create(context.Background(), "u", "x")
	s := &service.Feedback{
		Convs:    convs,
		Feedback: fb,
		Msgs:     msgs,
		JEV:      jev.New(config.Config{}, zerolog.Nop()),
		Logger:   testutil.QuietLogger(),
	}
	_ = jev.New(config.Config{}, zerolog.Nop())
	if _, err := s.Submit(context.Background(), service.FeedbackInput{ConversationID: c.ID, Rating: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(context.Background(), service.FeedbackInput{ConversationID: c.ID, Rating: 9}); err == nil {
		t.Fatalf("expected validation error")
	}
	if _, err := s.Submit(context.Background(), service.FeedbackInput{ConversationID: "missing", Rating: 3}); err == nil {
		t.Fatalf("expected not-found error")
	}
}

// silence unused
var _ = llm.RoleUser