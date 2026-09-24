// Package repo wraps SQL access for all entities. Each repository struct
// owns its own queries and never returns *sql.Rows to callers; everything
// is materialised into model structs to keep the service layer free of
// database plumbing.
package repo

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"

	"intelligent_customer/backend/internal/model"
	"intelligent_customer/backend/internal/security"
)

type Messages struct {
	DB     *sql.DB
	encKey []byte
}

// Conversations, FAQs, etc. follow further below.

func (r *Messages) WithEncryption(key []byte) *Messages { r.encKey = key; return r }

type Conversations struct{ DB *sql.DB }

func NewConversations(db *sql.DB) *Conversations { return &Conversations{DB: db} }

func (r *Conversations) Create(ctx context.Context, userID, title string) (*model.Conversation, error) {
	now := time.Now().UnixMilli()
	c := &model.Conversation{
		ID:        uuid.NewString(),
		UserID:    userID,
		Title:     title,
		Status:    model.ConvStatusOpen,
		CreatedAt: time.UnixMilli(now),
		UpdatedAt: time.UnixMilli(now),
	}
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO conversations(id,user_id,title,status,handed_over,created_at,updated_at)
		 VALUES(?,?,?,?,0,?,?)`,
		c.ID, c.UserID, c.Title, string(c.Status), now, now)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (r *Conversations) Get(ctx context.Context, id string) (*model.Conversation, error) {
	row := r.DB.QueryRowContext(ctx,
		`SELECT id,user_id,title,status,handed_over,created_at,updated_at FROM conversations WHERE id=?`, id)
	return scanConversation(row)
}

func (r *Conversations) Touch(ctx context.Context, id string) error {
	_, err := r.DB.ExecContext(ctx,
		`UPDATE conversations SET updated_at=? WHERE id=?`, time.Now().UnixMilli(), id)
	return err
}

func (r *Conversations) SetHandedOver(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()
	_, err := r.DB.ExecContext(ctx,
		`UPDATE conversations SET handed_over=1, status=?, updated_at=? WHERE id=?`,
		string(model.ConvStatusHandedOver), now, id)
	return err
}

func (r *Conversations) Close(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()
	_, err := r.DB.ExecContext(ctx,
		`UPDATE conversations SET status=?, updated_at=? WHERE id=?`,
		string(model.ConvStatusClosed), now, id)
	return err
}

// ListAdmin returns conversations with pagination, newest first.
type ConvFilter struct {
	Status model.ConvStatus // empty = any
	Limit  int
	Offset int
}

func (r *Conversations) ListAdmin(ctx context.Context, f ConvFilter) ([]model.Conversation, int, error) {
	args := []any{}
	whereClause := ""
	if f.Status != "" {
		whereClause = "WHERE status = ?"
		args = append(args, string(f.Status))
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	var total int
	if err := r.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM conversations `+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, f.Limit, f.Offset)
	rows, err := r.DB.QueryContext(ctx,
		`SELECT id,user_id,title,status,handed_over,created_at,updated_at
		 FROM conversations `+whereClause+` ORDER BY updated_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]model.Conversation, 0, f.Limit)
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *c)
	}
	return out, total, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanConversation(s rowScanner) (*model.Conversation, error) {
	var (
		c          model.Conversation
		status     string
		handed     int
		createdAt  int64
		updatedAt  int64
	)
	err := s.Scan(&c.ID, &c.UserID, &c.Title, &status, &handed, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	c.Status = model.ConvStatus(status)
	c.HandedOver = handed == 1
	c.CreatedAt = time.UnixMilli(createdAt)
	c.UpdatedAt = time.UnixMilli(updatedAt)
	return &c, nil
}

// ---------------------------------------------------------------------------

func NewMessages(db *sql.DB) *Messages { return &Messages{DB: db} }

// DBConn exposes the underlying *sql.DB for callers (such as service) that
// need to issue ad-hoc updates outside the prepared query surface.
func (r *Messages) DBConn() *sql.DB { return r.DB }

func (r *Messages) Insert(ctx context.Context, m *model.Message) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	content := m.Content
	if len(r.encKey) > 0 {
		ct, err := security.EncryptString(r.encKey, content)
		if err != nil {
			return err
		}
		content = ct
	}
	var conf *float64
	if m.IntentConfidence != nil {
		conf = m.IntentConfidence
	}
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO messages(id,conversation_id,role,content,intent,intent_confidence,model,created_at)
		 VALUES(?,?,?,?,?,?,?,?)`,
		m.ID, m.ConversationID, string(m.Role), content,
		nullStr(string(m.Intent)), conf, m.Model, m.CreatedAt.UnixMilli())
	return err
}

// scanRow reads one message row and applies decryption if a key is set.
func (r *Messages) scanRow(s interface {
	Scan(...any) error
}) (model.Message, error) {
	var (
		m      model.Message
		role   string
		intent sql.NullString
		conf   sql.NullFloat64
		model_ sql.NullString
		ts     int64
		ct     string
	)
	if err := s.Scan(&m.ID, &m.ConversationID, &role, &ct, &intent, &conf, &model_, &ts); err != nil {
		return m, err
	}
	if len(r.encKey) > 0 && ct != "" {
		pt, err := security.DecryptString(r.encKey, ct)
		if err != nil {
			return m, err
		}
		m.Content = pt
	} else {
		m.Content = ct
	}
	m.Role = model.MessageRole(role)
	if intent.Valid {
		m.Intent = model.Intent(intent.String)
	}
	if conf.Valid {
		v := conf.Float64
		m.IntentConfidence = &v
	}
	if model_.Valid {
		m.Model = model_.String
	}
	m.CreatedAt = time.UnixMilli(ts)
	return m, nil
}

func (r *Messages) ListByConversation(ctx context.Context, convID string) ([]model.Message, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT id,conversation_id,role,content,intent,intent_confidence,model,created_at
		 FROM messages WHERE conversation_id=? ORDER BY created_at ASC`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Message, 0)
	for rows.Next() {
		m, err := r.scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// LastN returns the most recent N messages for prompt construction.
func (r *Messages) LastN(ctx context.Context, convID string, n int) ([]model.Message, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT id,conversation_id,role,content,intent,intent_confidence,model,created_at
		 FROM messages WHERE conversation_id=? ORDER BY created_at DESC LIMIT ?`, convID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Message, 0, n)
	for rows.Next() {
		m, err := r.scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	// Reverse to chronological order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------

type Feedback struct {
	DB     *sql.DB
	encKey []byte
}

func (r *Feedback) WithEncryption(key []byte) *Feedback { r.encKey = key; return r }

func NewFeedback(db *sql.DB) *Feedback { return &Feedback{DB: db} }

func (r *Feedback) Upsert(ctx context.Context, f *model.Feedback) error {
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now()
	}
	comment := f.Comment
	if len(r.encKey) > 0 {
		ct, err := security.EncryptString(r.encKey, comment)
		if err != nil {
			return err
		}
		comment = ct
	}
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO feedback(id,conversation_id,rating,comment,created_at) VALUES(?,?,?,?,?)
		 ON CONFLICT(conversation_id) DO UPDATE SET rating=excluded.rating, comment=excluded.comment, created_at=excluded.created_at`,
		f.ID, f.ConversationID, f.Rating, comment, f.CreatedAt.UnixMilli())
	return err
}

// ---------------------------------------------------------------------------

type FAQs struct{ DB *sql.DB }

func NewFAQs(db *sql.DB) *FAQs { return &FAQs{DB: db} }

// ListEnabled returns all enabled FAQs, optionally filtered by category.
func (r *FAQs) ListEnabled(ctx context.Context, category string) ([]model.FAQ, error) {
	args := []any{1}
	q := `SELECT id,category,question,answer,keywords,enabled,created_at FROM faqs WHERE enabled=?`
	if category != "" {
		q += ` AND category=?`
		args = append(args, category)
	}
	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.FAQ, 0)
	for rows.Next() {
		var (
			f     model.FAQ
			cat   string
			kws   string
			en    int
			ts    int64
		)
		if err := rows.Scan(&f.ID, &cat, &f.Question, &f.Answer, &kws, &en, &ts); err != nil {
			return nil, err
		}
		f.Category = model.Intent(cat)
		f.Enabled = en == 1
		f.Keywords = splitTrim(kws, ",")
		f.CreatedAt = time.UnixMilli(ts)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *FAQs) Get(ctx context.Context, id string) (*model.FAQ, error) {
	row := r.DB.QueryRowContext(ctx,
		`SELECT id,category,question,answer,keywords,enabled,created_at FROM faqs WHERE id=?`, id)
	var (
		f   model.FAQ
		cat string
		kws string
		en  int
		ts  int64
	)
	if err := row.Scan(&f.ID, &cat, &f.Question, &f.Answer, &kws, &en, &ts); err != nil {
		return nil, err
	}
	f.Category = model.Intent(cat)
	f.Enabled = en == 1
	f.Keywords = splitTrim(kws, ",")
	f.CreatedAt = time.UnixMilli(ts)
	return &f, nil
}

// ---------------------------------------------------------------------------

type HandoverSignals struct{ DB *sql.DB }

func NewHandoverSignals(db *sql.DB) *HandoverSignals { return &HandoverSignals{DB: db} }

func (r *HandoverSignals) Insert(ctx context.Context, s *model.HandoverSignal) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO handover_signals(id,conversation_id,source,detail,created_at) VALUES(?,?,?,?,?)`,
		s.ID, s.ConversationID, s.Source, s.Detail, s.CreatedAt.UnixMilli())
	return err
}

func (r *HandoverSignals) CountByConv(ctx context.Context, convID string) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM handover_signals WHERE conversation_id=?`, convID).Scan(&n)
	return n, err
}

// ---------------------------------------------------------------------------

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func splitTrim(s, sep string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}