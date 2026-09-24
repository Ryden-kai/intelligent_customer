package repo_test

import (
	"context"
	"testing"
	"time"

	"intelligent_customer/backend/internal/repo"
	"intelligent_customer/backend/internal/testutil"
)

func TestNoncesInsertOnce(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	n := repo.NewNonces(conn)
	ctx := context.Background()

	ok, err := n.InsertOnce(ctx, "nonce-a", time.Now().Add(time.Minute).UnixMilli())
	if err != nil || !ok {
		t.Fatalf("first insert: ok=%v err=%v", ok, err)
	}
	ok, err = n.InsertOnce(ctx, "nonce-a", time.Now().Add(time.Minute).UnixMilli())
	if err != nil {
		t.Fatalf("dup insert err: %v", err)
	}
	if ok {
		t.Fatalf("expected dup to return false")
	}
	ok, _ = n.InsertOnce(ctx, "nonce-b", time.Now().Add(time.Minute).UnixMilli())
	if !ok {
		t.Fatalf("distinct nonce should be fresh")
	}
}

func TestNoncesPrune(t *testing.T) {
	conn, cleanup := testutil.OpenTempSQLite(t)
	defer cleanup()
	n := repo.NewNonces(conn)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, _ = n.InsertOnce(ctx, "old-"+string(rune('a'+i)), time.Now().Add(-time.Hour).UnixMilli())
		_, _ = n.InsertOnce(ctx, "fresh-"+string(rune('a'+i)), time.Now().Add(time.Hour).UnixMilli())
	}
	del, err := n.PruneExpired(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if del != 5 {
		t.Fatalf("expected to prune 5, got %d", del)
	}
}
