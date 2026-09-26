// Package auth issues and validates JWT tokens for admin endpoints. We use
// HS256 with the JWT_SECRET env var; secret rotation is out of scope.
//
// v2.2 PR1 扩展：
//   - Claims 新增 Email / Permissions / TenantID 字段（兼容旧 token：仅 sub+role）。
//   - Sign(username, role) 保留向后兼容；新增 SignWithPermissions(...) 用于新签 token。
//   - Parse(...) 对老 token 走"宽容路径"：
//       * 老 admin token（role=admin，无 permissions）→ permissions=["*"]
//       * 普通老 token → permissions=["chat.use","feedback.submit"], role="user"
//     具体判断依据：是否有 admin_users 行存在（见 context.go 的 adminLookup 注入）。
package auth

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"intelligent_customer/backend/internal/apperr"
)

// Claims 是 JWT payload 的 Go 表示。
//
// 字段顺序：v2.1.1 已有 Username（sub）+ Role；v2.2 扩展为：
//   - Email       用户的 email（可空，仅 admin 用）。
//   - Permissions 权限码列表（v2.2 新增）。
//   - TenantID    租户 id（v2.2 新增，与 X-Tenant-ID 对齐）。
//
// JSON 输出策略：
//   - 老 Sign() 创建的 token：不输出 permissions 字段（与 v2.1.1 完全兼容）。
//   - 新 SignWithPermissions() 创建的 token：始终输出 permissions 字段，
//     即使是空数组也输出 `[]`（与 nil 区分，PR2+ 业务层依赖此区分）。
//
// 实现：通过自定义 MarshalJSON + 内部 includePerms 标志控制输出。
type Claims struct {
	Username  string   `json:"sub"`
	Email     string   `json:"email,omitempty"`
	Role      string   `json:"role,omitempty"`
	TenantID  string   `json:"tid,omitempty"`
	includePerms bool  // unexported：是否输出 permissions 字段
	Permissions []string `json:"-"`
	jwt.RegisteredClaims
}

// MarshalJSON 自定义 JSON 序列化：includePerms=true 时输出 permissions 字段。
func (c Claims) MarshalJSON() ([]byte, error) {
	type Alias Claims
	if c.includePerms {
		return json.Marshal(&struct {
			Alias
			Permissions []string `json:"permissions"`
		}{
			Alias:       Alias(c),
			Permissions: c.Permissions,
		})
	}
	// 老 token：省略 permissions 字段。
	return json.Marshal(Alias(c))
}

// UnmarshalJSON 自定义 JSON 反序列化：从 JWT payload 中读取 permissions 字段。
//
// 由于 Permissions 用了 `json:"-"` 标签（避免老 token 写出 `"permissions":null`），
// 默认 json.Unmarshal 不会填充它。这里补上：解析时把 permissions 字段从 JSON 中取出。
func (c *Claims) UnmarshalJSON(b []byte) error {
	type Alias Claims
	aux := &struct {
		Alias
		Permissions []string `json:"permissions"`
	}{
		Alias: Alias(*c),
	}
	if err := json.Unmarshal(b, aux); err != nil {
		return err
	}
	*c = Claims(aux.Alias)
	c.Permissions = aux.Permissions
	return nil
}

// Issuer 持有 secret + TTL，签发/解析 token。
type Issuer struct {
	secret []byte
	ttl    time.Duration
	// adminLookup 用于"老 token 兼容路径"：解析时若发现 token 没有 permissions 字段，
	// 通过 adminLookup(username) 判断是否是 admin 账号。
	// 可选；当为 nil 时，所有无 permissions 的老 token 都被当作普通 user（role=user）。
	adminLookup func(username string) bool
}

// NewIssuer 构造 Issuer（不带 adminLookup；保留向后兼容签名）。
func NewIssuer(secret string, ttl time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), ttl: ttl}
}

// NewIssuerWithAdminLookup 构造带 adminLookup 的 Issuer。
// adminLookup 接收 username，返回该用户是否为 admin 账号（true=admin）。
// 用于"老 token 兼容路径"：解析无 permissions 字段的老 token 时决定权限集。
func NewIssuerWithAdminLookup(secret string, ttl time.Duration, adminLookup func(username string) bool) *Issuer {
	return &Issuer{
		secret:      []byte(secret),
		ttl:         ttl,
		adminLookup: adminLookup,
	}
}

// Sign 签发老格式 token（仅 sub + role + 标准 claims，无 permissions）。
// 保留向后兼容：v2.1.1 admin 登录路径仍走这里。
func (i *Issuer) Sign(username, role string) (string, error) {
	return i.signClaims(username, "", role, nil, "", false)
}

// SignWithPermissions 签发新格式 token：包含 permissions + 可选 email/tenantID。
//
// permissions 不可为 nil；若传 nil 会被替换为空切片以保证 JSON 输出是 [] 而非 null。
func (i *Issuer) SignWithPermissions(username, email, role, tenantID string, permissions []string) (string, error) {
	if permissions == nil {
		permissions = []string{}
	}
	return i.signClaims(username, email, role, permissions, tenantID, true)
}

// signClaims 是 sign 的内部实现。includePerms=false 时省略 permissions 字段以模拟"老 token"。
func (i *Issuer) signClaims(username, email, role string, perms []string, tenantID string, includePerms bool) (string, error) {
	now := time.Now()
	c := Claims{
		Username:      username,
		Email:         email,
		Role:          role,
		TenantID:      tenantID,
		includePerms:  includePerms,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			Subject:   username,
		},
	}
	if includePerms {
		c.Permissions = perms
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return t.SignedString(i.secret)
}

// Parse 解析 token 并填充 Claims。自动处理老 token 兼容路径：
//   1. 老 token（无 permissions 字段）：
//      - adminLookup(username) == true → 注入 role=admin, permissions=["*"]
//      - 否则 → 注入 role=user, permissions=["chat.use","feedback.submit"]
//   2. 新 token：保留 sign 时的 permissions / role / email / tenantID。
//
// 兼容路径不会覆盖 token 中已有的 Role / Permissions 字段。
func (i *Issuer) Parse(token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return i.secret, nil
	})
	if err != nil {
		return nil, apperr.Unauthorized("invalid token").WithCause(err)
	}
	c, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, apperr.Unauthorized("invalid claims")
	}
	// 老 token 兼容路径：permissions 字段为空 → 视为老 token，走保守降级。
	//   - 有 adminLookup 且 username 是 admin：注入 role=admin, permissions=["*"]
	//   - 否则（含无 adminLookup 的安全模式）：统一降级为 role=user, permissions=[...]
	//     即便原 token 中已有 role="admin" 也降级，避免越权。
	if len(c.Permissions) == 0 {
		isAdmin := i.adminLookup != nil && i.adminLookup(c.Username)
		if isAdmin {
			c.Role = "admin"
			c.Permissions = []string{"*"}
		} else {
			c.Role = "user"
			c.Permissions = []string{"chat.use", "feedback.submit"}
		}
	}
	return c, nil
}

// TTL exposes the configured lifetime so handlers can show it to clients.
func (i *Issuer) TTL() time.Duration { return i.ttl }