package audit

import (
	"context"
	"testing"
	"time"

	"intelligent_customer/backend/internal/auth"
)

// TestRepo_InsertBatch_Happy 验证 InsertBatch 成功落库。
func TestRepo_InsertBatch_Happy(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)

	now := time.Now()
	events := []Event{
		{ID: "log-rb-1", TenantID: "tnt_default", Timestamp: now, ActorID: "alice", Action: ActionAuthLogin},
		{ID: "log-rb-2", TenantID: "tnt_default", Timestamp: now, ActorID: "bob", Action: ActionSkillCreate},
	}
	if err := repo.InsertBatch(context.Background(), events); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	logs, total, err := repo.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 {
		t.Errorf("total=%d, want 2", total)
	}
	if len(logs) != 2 {
		t.Errorf("len(logs)=%d, want 2", len(logs))
	}
}

// TestRepo_InsertBatch_Empty 验证空 events 不报错。
func TestRepo_InsertBatch_Empty(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	if err := repo.InsertBatch(context.Background(), nil); err != nil {
		t.Errorf("empty batch should not error: %v", err)
	}
}

// TestRepo_List_FilterByAction 验证按 action 筛选。
func TestRepo_List_FilterByAction(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	now := time.Now()
	events := []Event{
		{ID: "log-fa-1", Timestamp: now, ActorID: "alice", Action: ActionAuthLogin},
		{ID: "log-fa-2", Timestamp: now, ActorID: "bob", Action: ActionSkillCreate},
	}
	if err := repo.InsertBatch(context.Background(), events); err != nil {
		t.Fatalf("seed: %v", err)
	}
	logs, total, err := repo.List(context.Background(), Filter{Action: ActionSkillCreate})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 {
		t.Errorf("total=%d, want 1", total)
	}
	if logs[0].Action != ActionSkillCreate {
		t.Errorf("action=%s, want %s", logs[0].Action, ActionSkillCreate)
	}
}

// TestRepo_List_FilterByActor 验证按 actor 筛选。
func TestRepo_List_FilterByActor(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	now := time.Now()
	events := []Event{
		{ID: "log-fac-1", Timestamp: now, ActorID: "alice", Action: ActionAuthLogin},
		{ID: "log-fac-2", Timestamp: now, ActorID: "bob", Action: ActionSkillCreate},
	}
	if err := repo.InsertBatch(context.Background(), events); err != nil {
		t.Fatalf("seed: %v", err)
	}
	logs, total, err := repo.List(context.Background(), Filter{ActorID: "alice"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || logs[0].ActorID != "alice" {
		t.Errorf("got %d logs, want alice only", total)
	}
}

// TestRepo_List_FilterByTimeRange 验证时间范围筛选。
func TestRepo_List_FilterByTimeRange(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	old := time.Now().Add(-48 * time.Hour)
	fresh := time.Now()
	if err := repo.InsertBatch(context.Background(), []Event{
		{ID: "log-tr-old", Timestamp: old, ActorID: "x", Action: ActionAuthLogin},
		{ID: "log-tr-new", Timestamp: fresh, ActorID: "y", Action: ActionSkillCreate},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	logs, total, err := repo.List(context.Background(), Filter{
		From: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || logs[0].ID != "log-tr-new" {
		t.Errorf("time filter failed: total=%d logs=%v", total, logs)
	}
}

// TestRepo_Get_OK 验证按 ID 取单条。
func TestRepo_Get_OK(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	if err := repo.InsertBatch(context.Background(), []Event{
		{ID: "log-get-1", Timestamp: time.Now(), ActorID: "x", Action: ActionAuthLogin},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := repo.Get(context.Background(), "log-get-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Action != ActionAuthLogin {
		t.Errorf("action=%s", got.Action)
	}
}

// TestRepo_Get_NotFound 验证找不到时返回 ErrNotFound。
func TestRepo_Get_NotFound(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	_, err := repo.Get(context.Background(), "log-does-not-exist")
	if err != ErrNotFound {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

// TestRepo_ExportCSV_HasBOM 验证 CSV 含 BOM。
func TestRepo_ExportCSV_HasBOM(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	if err := repo.InsertBatch(context.Background(), []Event{
		{ID: "log-bom-1", Timestamp: time.Now(), ActorID: "x", Action: ActionAuthLogin},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	buf, err := repo.ExportCSV(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("ExportCSV: %v", err)
	}
	if len(buf.Bytes()) < 3 || buf.Bytes()[0] != 0xEF || buf.Bytes()[1] != 0xBB || buf.Bytes()[2] != 0xBF {
		t.Errorf("missing BOM in CSV")
	}
}

// TestRepo_ExportCSV_EscapesCommas 验证 CSV 转义逗号。
func TestRepo_ExportCSV_EscapesCommas(t *testing.T) {
	conn := openTempDB(t)
	repo := NewRepo(conn)
	if err := repo.InsertBatch(context.Background(), []Event{
		{ID: "log-comma", Timestamp: time.Now(), ActorID: "x", ActorEmail: "a,b@c",
			Action: ActionAuthLogin, UserAgent: `Mozilla/5.0 (Windows NT 10.0; Win64; x64)`},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	buf, err := repo.ExportCSV(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("ExportCSV: %v", err)
	}
	body := buf.String()
	if !contains(body, `"a,b@c"`) {
		t.Errorf("CSV should escape comma in email: %s", body)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// keep auth import referenced (used via logger elsewhere)
var _ = auth.Claims{}