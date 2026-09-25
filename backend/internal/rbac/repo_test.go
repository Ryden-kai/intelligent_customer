package rbac_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"intelligent_customer/backend/internal/db"
	dbseed "intelligent_customer/backend/internal/db/seed"
	"intelligent_customer/backend/internal/rbac"
)

// openTempDB 应用 migration + RBAC seed 后返回 *sql.DB。
func openTempDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	conn, err := db.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(conn, db.MigrationsFS, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := dbseed.RunRBACSeed(context.Background(), conn); err != nil {
		t.Fatalf("rbac seed: %v", err)
	}
	return conn
}

// ----------------------------------------------------------------------------
//  Permission code 工具函数（10 用例）
// ----------------------------------------------------------------------------

func TestAllPermissions_Has25(t *testing.T) {
	if got := len(rbac.AllPermissions); got != 25 {
		t.Fatalf("AllPermissions len = %d, want 25", got)
	}
}

func TestPermissionGroups_HasAllCodes(t *testing.T) {
	seen := make(map[string]bool)
	for _, codes := range rbac.PermissionGroups {
		for _, c := range codes {
			seen[c] = true
		}
	}
	for _, p := range rbac.AllPermissions {
		if !seen[p] {
			t.Errorf("permission %s not present in any group", p)
		}
	}
}

func TestHasPermission_Wildcard(t *testing.T) {
	if !rbac.HasPermission([]string{"*"}, "anything") {
		t.Errorf("* should match any code")
	}
}

func TestHasPermission_ExactMatch(t *testing.T) {
	if !rbac.HasPermission([]string{"jev.template.read"}, "jev.template.read") {
		t.Errorf("exact match should pass")
	}
	if rbac.HasPermission([]string{"jev.template.read"}, "jev.template.write") {
		t.Errorf("different code should not match")
	}
}

func TestHasPermission_Empty(t *testing.T) {
	if rbac.HasPermission(nil, "x") {
		t.Errorf("nil perms should not match")
	}
	if rbac.HasPermission([]string{"x"}, "") {
		t.Errorf("empty code should not match")
	}
}

func TestValidatePermissionCodes_Valid(t *testing.T) {
	codes := []string{"jev.template.read", "conversation.read"}
	unknown, ok := rbac.ValidatePermissionCodes(codes)
	if !ok || len(unknown) != 0 {
		t.Errorf("valid codes should pass: unknown=%v", unknown)
	}
}

func TestValidatePermissionCodes_Invalid(t *testing.T) {
	codes := []string{"jev.template.read", "totally.bogus"}
	unknown, ok := rbac.ValidatePermissionCodes(codes)
	if ok {
		t.Errorf("invalid code should fail")
	}
	if len(unknown) != 1 || unknown[0] != "totally.bogus" {
		t.Errorf("unknown=%v, want [totally.bogus]", unknown)
	}
}

func TestValidatePermissionCodes_Empty(t *testing.T) {
	unknown, ok := rbac.ValidatePermissionCodes(nil)
	if !ok || len(unknown) != 0 {
		t.Errorf("empty input should pass")
	}
}

func TestValidatePermissionCodes_WildcardAccepted(t *testing.T) {
	unknown, ok := rbac.ValidatePermissionCodes([]string{"*"})
	if !ok || len(unknown) != 0 {
		t.Errorf("wildcard should pass: unknown=%v", unknown)
	}
}

func TestValidatePermissionCodes_DedupUnknown(t *testing.T) {
	unknown, ok := rbac.ValidatePermissionCodes([]string{"bogus", "bogus", "also_bogus"})
	if ok {
		t.Errorf("should fail")
	}
	// unknown 应保留每个独立未知码 1 次。
	if len(unknown) != 2 {
		t.Errorf("unknown=%v, want 2 entries", unknown)
	}
}

// ----------------------------------------------------------------------------
//  Repo CRUD（10 用例）
// ----------------------------------------------------------------------------

func TestRepo_ListPermissions_Returns25(t *testing.T) {
	conn := openTempDB(t)
	repo := rbac.NewRepo(conn)

	perms, err := repo.ListPermissions(context.Background())
	if err != nil {
		t.Fatalf("ListPermissions: %v", err)
	}
	if len(perms) != 25 {
		t.Errorf("got %d perms, want 25", len(perms))
	}
	// 至少包含 audit.read。
	for _, p := range perms {
		if p.Code == "audit.read" && p.GroupName != "audit" {
			t.Errorf("audit.read group = %q, want audit", p.GroupName)
		}
	}
}

func TestRepo_ListRoles_Returns3SystemRoles(t *testing.T) {
	conn := openTempDB(t)
	repo := rbac.NewRepo(conn)

	roles, err := repo.ListRoles(context.Background(), "tnt_default")
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	if len(roles) != 3 {
		t.Fatalf("got %d roles, want 3", len(roles))
	}
	// admin 应该是 is_system=true 且 permissions 含 "*"。
	var admin *rbac.Role
	for i := range roles {
		if roles[i].Name == "admin" {
			admin = &roles[i]
		}
	}
	if admin == nil {
		t.Fatalf("admin role missing")
	}
	if !admin.IsSystem {
		t.Errorf("admin should be system role")
	}
	hasWildcard := false
	for _, p := range admin.Permissions {
		if p == "*" {
			hasWildcard = true
		}
	}
	if !hasWildcard {
		t.Errorf("admin role missing '*' wildcard: perms=%v", admin.Permissions)
	}
}

func TestRepo_CreateRole_Happy(t *testing.T) {
	conn := openTempDB(t)
	repo := rbac.NewRepo(conn)

	created, err := repo.CreateRole(context.Background(), rbac.Role{
		TenantID:    "tnt_default",
		Name:        "viewer",
		Description: "Read-only viewer",
		Permissions: []string{"stats.read", "audit.read"},
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("empty id")
	}
	if !strings.HasPrefix(created.ID, "role-") {
		t.Errorf("id = %q, want role- prefix", created.ID)
	}

	got, err := repo.GetRole(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}
	if got.Name != "viewer" {
		t.Errorf("name = %q, want viewer", got.Name)
	}
}

func TestRepo_CreateRole_RejectsUnknownCode(t *testing.T) {
	conn := openTempDB(t)
	repo := rbac.NewRepo(conn)

	_, err := repo.CreateRole(context.Background(), rbac.Role{
		TenantID:    "tnt_default",
		Name:        "bad",
		Permissions: []string{"stats.read", "totally.bogus"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown permission codes") {
		t.Errorf("err = %v, want unknown permission codes", err)
	}
}

func TestRepo_CreateRole_RejectsEmptyPerms(t *testing.T) {
	conn := openTempDB(t)
	repo := rbac.NewRepo(conn)

	_, err := repo.CreateRole(context.Background(), rbac.Role{
		TenantID:    "tnt_default",
		Name:        "bad",
		Permissions: []string{},
	})
	if err == nil {
		t.Errorf("empty perms should fail")
	}
}

func TestRepo_DeleteRole_RejectsSystemRole(t *testing.T) {
	conn := openTempDB(t)
	repo := rbac.NewRepo(conn)

	err := repo.DeleteRole(context.Background(), "role-admin-tnt_default")
	if err == nil || !strings.Contains(err.Error(), "system-protected") {
		t.Errorf("err = %v, want system-protected", err)
	}
}

func TestRepo_DeleteRole_Happy(t *testing.T) {
	conn := openTempDB(t)
	repo := rbac.NewRepo(conn)

	created, err := repo.CreateRole(context.Background(), rbac.Role{
		TenantID:    "tnt_default",
		Name:        "tmp",
		Permissions: []string{"stats.read"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.DeleteRole(context.Background(), created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// 再查应返回 ErrRoleNotFound。
	if _, err := repo.GetRole(context.Background(), created.ID); err != rbac.ErrRoleNotFound {
		t.Errorf("after delete: err = %v, want ErrRoleNotFound", err)
	}
}

func TestRepo_UpdateRole_Happy(t *testing.T) {
	conn := openTempDB(t)
	repo := rbac.NewRepo(conn)

	created, err := repo.CreateRole(context.Background(), rbac.Role{
		TenantID:    "tnt_default",
		Name:        "tmp",
		Permissions: []string{"stats.read"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := repo.UpdateRole(context.Background(), created.ID, rbac.Role{
		Description: "Updated",
		Permissions: []string{"stats.read", "audit.read"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Description != "Updated" {
		t.Errorf("description = %q, want Updated", updated.Description)
	}
	if len(updated.Permissions) != 2 {
		t.Errorf("perms = %v, want 2 entries", updated.Permissions)
	}
}

func TestRepo_UpdateRole_RejectsSystemRename(t *testing.T) {
	conn := openTempDB(t)
	repo := rbac.NewRepo(conn)

	_, err := repo.UpdateRole(context.Background(), "role-admin-tnt_default", rbac.Role{
		Name: "renamed",
	})
	if err == nil || !strings.Contains(err.Error(), "system-protected") {
		t.Errorf("err = %v, want system-protected", err)
	}
}

func TestRepo_SetUserRoles_AndList(t *testing.T) {
	conn := openTempDB(t)
	repo := rbac.NewRepo(conn)

	// 把 agent 角色分配给 u-1。
	if err := repo.SetUserRoles(context.Background(), "u-1", "tnt_default",
		[]string{"role-agent-tnt_default"}); err != nil {
		t.Fatalf("SetUserRoles: %v", err)
	}

	roles, err := repo.GetUserRoles(context.Background(), "u-1")
	if err != nil {
		t.Fatalf("GetUserRoles: %v", err)
	}
	if len(roles) != 1 {
		t.Fatalf("got %d roles, want 1", len(roles))
	}
	if roles[0].Name != "agent" {
		t.Errorf("role name = %q, want agent", roles[0].Name)
	}

	// 重设：清空。
	if err := repo.SetUserRoles(context.Background(), "u-1", "", nil); err != nil {
		t.Fatalf("SetUserRoles clear: %v", err)
	}
	roles, _ = repo.GetUserRoles(context.Background(), "u-1")
	if len(roles) != 0 {
		t.Errorf("after clear: got %d roles, want 0", len(roles))
	}
}

func TestRepo_SetUserRoles_RejectsUnknownRoleID(t *testing.T) {
	conn := openTempDB(t)
	repo := rbac.NewRepo(conn)

	err := repo.SetUserRoles(context.Background(), "u-1", "tnt_default",
		[]string{"role-does-not-exist"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want not found", err)
	}
}