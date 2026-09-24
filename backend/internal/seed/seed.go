// Package seed inserts demo data on first boot:
//   1) FAQ rows into the faqs table
//   2) a single admin_users row, hashed with Argon2id, when the table is
//      empty and a Bootstrap username/password was provided.
//
// After the initial admin row exists it is never overwritten; further admin
// accounts are managed via the adminctl CLI.
package seed

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"

	"intelligent_customer/backend/internal/model"
	"intelligent_customer/backend/internal/security"
)

// Bootstrap holds the credentials that seed the very first admin row.
// Empty fields disable admin bootstrap (e.g. when an operator has already
// pre-seeded the table through adminctl).
type Bootstrap struct {
	AdminUsername string
	AdminPassword string
}

// Result is what Run returns so callers can log what was created.
type Result struct {
	FAQsInserted int
	AdminCreated bool // true when this run inserted the bootstrap admin
}

func faqSeedData() []struct {
	Category string
	Q        string
	A        string
	Keywords string
} {
	return []struct {
		Category string
		Q        string
		A        string
		Keywords string
	}{
		// 退款
		{"refund", "如何申请退款？", "您可以在订单详情页点击「申请退款」，选择退款原因后提交，我们会在 1-3 个工作日内审核处理。", "退款,申请,退钱,refund"},
		{"refund", "退款多久能到账？", "审核通过后，原路退回：支付宝/微信支付 1-3 个工作日，银行卡 3-7 个工作日。", "退款,到账,多久,时间"},
		{"refund", "不想要了可以退款吗？", "支持 7 天无理由退款，商品未拆封不影响二次销售即可。已拆封或影响二次销售的需协商。", "退款,无理由,不想要,退货"},
		// 订单
		{"order", "怎么查看我的订单？", "打开「我的-我的订单」，即可查看全部订单状态；也可输入订单号查询单个订单。", "订单,查询,查看,order"},
		{"order", "订单可以修改地址吗？", "未发货的订单可以在订单详情页修改收货地址；已发货的请拒收后联系商家。", "订单,地址,修改,收货"},
		{"order", "为什么我的订单还在处理中？", "通常 24 小时内会出库；如超过 48 小时仍未发货，可申请退款或联系客服。", "订单,处理,发货,延迟"},
		// 技术支持
		{"tech", "App 闪退 / 打不开怎么办？", "请尝试：1) 升级到最新版本 2) 清理缓存重启 3) 卸载重装。如仍异常，请反馈设备型号与系统版本。", "闪退,打不开,崩溃"},
		{"tech", "登录不上 / 验证码收不到？", "请检查手机信号、是否被拦截；若长时间未收到可改用「短信验证码登录」或「微信登录」。", "登录,验证码,登录失败"},
		{"tech", "支付失败了？", "可能原因：余额不足、卡片限额、银行风控。建议更换支付方式或稍后再试。", "支付,失败,扣款,付款"},
		// 通用
		{"general", "客服工作时间？", "在线客服：9:00 - 22:00；其他时段您可留言，我们会在次日回复。", "工作时间,在线,客服,时间"},
		{"general", "如何联系人工客服？", "在本对话框输入「转人工」或点击右下角「转人工」按钮，我们会立即安排。", "人工,转人工,真人,客服"},
	}
}

// Run is invoked by cmd/server after migrations. It is safe to call on every
// boot — every operation is gated by an "is the table empty?" check.
func Run(ctx context.Context, dbx *sql.DB, b Bootstrap) (Result, error) {
	out := Result{}

	// ---- FAQs ----
	var n int
	if err := dbx.QueryRowContext(ctx, `SELECT COUNT(*) FROM faqs`).Scan(&n); err != nil {
		return out, err
	}
	if n == 0 {
		faqs := faqSeedData()
		now := time.Now().UnixMilli()
		tx, err := dbx.BeginTx(ctx, nil)
		if err != nil {
			return out, err
		}
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO faqs(id,category,question,answer,keywords,enabled,created_at) VALUES(?,?,?,?,?,1,?)`)
		if err != nil {
			_ = tx.Rollback()
			return out, err
		}
		for _, f := range faqs {
			cat := f.Category
			if cat == "" {
				cat = string(model.IntentOther)
			}
			if _, err := stmt.ExecContext(ctx,
				uuid.NewString(), cat, f.Q, f.A, strings.TrimSpace(f.Keywords), now); err != nil {
				_ = stmt.Close()
				_ = tx.Rollback()
				return out, err
			}
		}
		_ = stmt.Close()
		if err := tx.Commit(); err != nil {
			return out, err
		}
		out.FAQsInserted = len(faqs)
	}

	// ---- Bootstrap admin ----
	// Skip when username/password is missing (operator already set things
	// up) or when admin_users is non-empty.
	if b.AdminUsername == "" || b.AdminPassword == "" {
		return out, nil
	}
	var adminCount int
	if err := dbx.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users`).Scan(&adminCount); err != nil {
		return out, err
	}
	if adminCount > 0 {
		return out, nil
	}

	hash, err := security.HashPassword(b.AdminPassword)
	if err != nil {
		return out, err
	}
	now := time.Now().UnixMilli()
	_, err = dbx.ExecContext(ctx,
		`INSERT INTO admin_users(id, username, password_hash, role, created_at, updated_at)
		 VALUES(?,?,?,?,?,?)`,
		uuid.NewString(), b.AdminUsername, hash, "admin", now, now)
	if err != nil {
		return out, err
	}
	out.AdminCreated = true
	return out, nil
}