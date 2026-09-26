package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const testSecret = "test-secret-please-replace-with-real-32-bytes"

// TestSignParseRoundTrip 验证老格式 token（v2.1.1 风格：无 permissions 字段）
// 经 Parse 后走兼容路径：没 adminLookup 时降级为 user。
//
// 注：v2.2 PR1 的语义变化——为了防御旧 admin token 越权，没注入 adminLookup 的
// Issuer 会把任意老 token 降级为 role=user，避免被错误识别为 admin。
// 注入 adminLookup 的测试见 TestParse_OldToken_AdminLookup_SetsAdminWildcard。
func TestSignParseRoundTrip(t *testing.T) {
	iss := NewIssuer(testSecret, time.Hour)
	tok, err := iss.Sign("alice", "admin")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if tok == "" {
		t.Fatalf("empty token")
	}
	c, err := iss.Parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Username != "alice" {
		t.Fatalf("username wrong: %+v", c)
	}
	// v2.2 兼容路径：no adminLookup → 降级到 user。
	if c.Role != "user" {
		t.Errorf("Role = %q, want user (compat path)", c.Role)
	}
	want := []string{"chat.use", "feedback.submit"}
	if !sliceEq(c.Permissions, want) {
		t.Errorf("Permissions = %v, want %v", c.Permissions, want)
	}
}

// TestParseRejectsTampered 验证签名被篡改时拒绝。
func TestParseRejectsTampered(t *testing.T) {
	iss := NewIssuer(testSecret, time.Hour)
	tok, err := iss.Sign("alice", "admin")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	bad := tok[:len(tok)-2] + "XX"
	if _, err := iss.Parse(bad); err == nil {
		t.Fatalf("expected error on tampered token")
	}
}

// TestParseRejectsWrongSecret 验证不同 secret 无法解析。
func TestParseRejectsWrongSecret(t *testing.T) {
	a := NewIssuer(testSecret, time.Hour)
	b := NewIssuer("completely-different-secret-still-32+bytes-long", time.Hour)
	tok, _ := a.Sign("alice", "admin")
	if _, err := b.Parse(tok); err == nil {
		t.Fatalf("expected error with wrong secret")
	}
}

// TestParseRejectsExpired 验证过期 token 被拒。
func TestParseRejectsExpired(t *testing.T) {
	iss := NewIssuer(testSecret, -time.Second)
	tok, _ := iss.Sign("alice", "admin")
	if _, err := iss.Parse(tok); err == nil {
		t.Fatalf("expected error on expired token")
	}
}

// ----------------------------------------------------------------------------
// v2.2 PR1 新增测试：JWT claims 扩展 + 兼容路径
// ----------------------------------------------------------------------------

// TestSignWithPermissions_NewToken 验证新 token 携带 permissions + email + tenantID。
func TestSignWithPermissions_NewToken(t *testing.T) {
	iss := NewIssuer(testSecret, time.Hour)
	tok, err := iss.SignWithPermissions(
		"alice", "alice@demo", "agent", "tnt_acme",
		[]string{"conversation.read", "skills.read", "stats.read"},
	)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	c, err := iss.Parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Username != "alice" {
		t.Errorf("Username = %q, want alice", c.Username)
	}
	if c.Email != "alice@demo" {
		t.Errorf("Email = %q, want alice@demo", c.Email)
	}
	if c.Role != "agent" {
		t.Errorf("Role = %q, want agent", c.Role)
	}
	if c.TenantID != "tnt_acme" {
		t.Errorf("TenantID = %q, want tnt_acme", c.TenantID)
	}
	want := map[string]bool{
		"conversation.read": true,
		"skills.read":       true,
		"stats.read":        true,
	}
	if len(c.Permissions) != len(want) {
		t.Fatalf("Permissions len = %d, want %d", len(c.Permissions), len(want))
	}
	for _, p := range c.Permissions {
		if !want[p] {
			t.Errorf("unexpected permission %q", p)
		}
	}
}

// TestSignWithPermissions_NilBecomesEmptyArray 验证 nil permissions 序列化为空数组。
func TestSignWithPermissions_NilBecomesEmptyArray(t *testing.T) {
	iss := NewIssuer(testSecret, time.Hour)
	tok, err := iss.SignWithPermissions("alice", "", "user", "", nil)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// 解析 raw payload 检查 JSON 是否为 [] 而非 null。
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("malformed token")
	}
	// payload 是 base64url；解码后必须包含 [] 而非 null。
	payload, err := jwtDecodePayload(parts[1])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(payload, `"permissions":[]`) {
		t.Errorf("payload should contain empty array, got: %s", payload)
	}
}

// TestParse_OldToken_NoAdminLookup_DefaultsToUser 验证不带 adminLookup 时，
// 老 token（仅 sub+role，无 permissions）走 user 兼容路径。
func TestParse_OldToken_NoAdminLookup_DefaultsToUser(t *testing.T) {
	iss := NewIssuer(testSecret, time.Hour)
	// 老格式：Sign("alice", "user") 出来的 token 没有 permissions 字段。
	tok, err := iss.Sign("alice", "user")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	c, err := iss.Parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Role != "user" {
		t.Errorf("Role = %q, want user", c.Role)
	}
	want := []string{"chat.use", "feedback.submit"}
	if !sliceEq(c.Permissions, want) {
		t.Errorf("Permissions = %v, want %v", c.Permissions, want)
	}
}

// TestParse_OldToken_NoAdminLookup_AdminRoleKeepsAdmin 验证老 token 即使 role=admin，
// 但没有 adminLookup 时仍走 user 兼容路径（保守）。
func TestParse_OldToken_NoAdminLookup_AdminRoleKeepsAdmin(t *testing.T) {
	iss := NewIssuer(testSecret, time.Hour)
	tok, _ := iss.Sign("alice", "admin") // role=admin 但 permissions=空
	c, err := iss.Parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 没有 adminLookup，无法验证 alice 是不是 admin；保守降级为 user。
	if c.Role != "user" {
		t.Errorf("Role = %q, want user (no adminLookup)", c.Role)
	}
}

// TestParse_OldToken_AdminLookup_SetsAdminWildcard 验证带 adminLookup 时，
// admin 账号的老 token 被注入 ["*"] permissions。
func TestParse_OldToken_AdminLookup_SetsAdminWildcard(t *testing.T) {
	// adminLookup：alice 在 admin_users 表里。
	adminLookup := func(username string) bool {
		return username == "alice"
	}
	iss := NewIssuerWithAdminLookup(testSecret, time.Hour, adminLookup)
	tok, _ := iss.Sign("alice", "user") // 即使老 token role=user，也会被覆盖
	c, err := iss.Parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Role != "admin" {
		t.Errorf("Role = %q, want admin", c.Role)
	}
	if !sliceEq(c.Permissions, []string{"*"}) {
		t.Errorf("Permissions = %v, want [*]", c.Permissions)
	}
}

// TestParse_OldToken_AdminLookup_NonAdminUser 验证带 adminLookup 时，
// 非 admin 账号走 user 兼容路径。
func TestParse_OldToken_AdminLookup_NonAdminUser(t *testing.T) {
	adminLookup := func(username string) bool {
		return username == "alice"
	}
	iss := NewIssuerWithAdminLookup(testSecret, time.Hour, adminLookup)
	tok, _ := iss.Sign("bob", "user") // bob 不是 admin
	c, err := iss.Parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Role != "user" {
		t.Errorf("Role = %q, want user", c.Role)
	}
	want := []string{"chat.use", "feedback.submit"}
	if !sliceEq(c.Permissions, want) {
		t.Errorf("Permissions = %v, want %v", c.Permissions, want)
	}
}

// TestParse_NewToken_DoesNotOverride 验证新 token 的 permissions 不会被兼容路径覆盖。
func TestParse_NewToken_DoesNotOverride(t *testing.T) {
	adminLookup := func(username string) bool {
		return username == "alice"
	}
	iss := NewIssuerWithAdminLookup(testSecret, time.Hour, adminLookup)
	tok, _ := iss.SignWithPermissions("alice", "", "agent", "",
		[]string{"conversation.read", "skills.read"})
	c, err := iss.Parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// alice 是 admin，但新 token 明确指定 role=agent + 精确 permissions；
	// 兼容路径不应该覆盖。
	if c.Role != "agent" {
		t.Errorf("Role = %q, want agent (new token preserved)", c.Role)
	}
	if !sliceEq(c.Permissions, []string{"conversation.read", "skills.read"}) {
		t.Errorf("Permissions = %v, want [conversation.read skills.read]",
			c.Permissions)
	}
}

// TestSign_OldFormat_NoPermissionsField 验证 Sign() 不写入 permissions 字段。
func TestSign_OldFormat_NoPermissionsField(t *testing.T) {
	iss := NewIssuer(testSecret, time.Hour)
	tok, _ := iss.Sign("alice", "admin")
	parts := strings.Split(tok, ".")
	payload, _ := jwtDecodePayload(parts[1])
	if strings.Contains(payload, "permissions") {
		t.Errorf("old Sign() should not emit permissions field, payload: %s", payload)
	}
}

// TestSignWithPermissions_NilIsNotJSONNull 验证 nil permissions 在 JWT JSON 中序列化为空数组（而非 null）。
// 注：Parse 后 Permissions 会被兼容路径覆盖为 [chat.use, feedback.submit]，
//     所以这里只检查 token 的 raw JSON payload，不检查 Parse 结果。
func TestSignWithPermissions_NilIsNotJSONNull(t *testing.T) {
	iss := NewIssuer(testSecret, time.Hour)
	tok, err := iss.SignWithPermissions("alice", "", "user", "", nil)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	parts := strings.Split(tok, ".")
	payload, err := jwtDecodePayload(parts[1])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(payload, `"permissions":[]`) {
		t.Errorf("payload should contain empty array (not null), got: %s", payload)
	}
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// jwtDecodePayload 用 base64url 解码 JWT payload（中间那段）。
func jwtDecodePayload(b64 string) (string, error) {
	// 补齐 padding。
	for len(b64)%4 != 0 {
		b64 += "="
	}
	dec, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		// 标准 base64 失败时尝试 url-safe（不加 padding 时的差异）。
		dec, err = base64.RawURLEncoding.DecodeString(b64)
		if err != nil {
			return "", err
		}
	}
	// 验证是合法 JSON。
	var any_ map[string]any
	if err := json.Unmarshal(dec, &any_); err != nil {
		return "", err
	}
	return string(dec), nil
}