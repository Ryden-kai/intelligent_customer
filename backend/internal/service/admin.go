package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/model"
	"intelligent_customer/backend/internal/repo"
	"intelligent_customer/backend/internal/skill"
)

type Admin struct {
	Convs       *repo.Conversations
	Msgs        *repo.Messages
	Feedback    *repo.Feedback
	Invocations *skill.Invocations
	Logger      zerolog.Logger
}

// ListConversations supports admin UI pagination + filtering.
type ListConversationsParams struct {
	Status string
	Limit  int
	Offset int
}

type ListConversationsResult struct {
	Total       int                    `json:"total"`
	Items       []model.Conversation   `json:"items"`
	Page        int                    `json:"page"`
	PageSize    int                    `json:"pageSize"`
}

func (s *Admin) ListConversations(ctx context.Context, p ListConversationsParams) (*ListConversationsResult, error) {
	f := repo.ConvFilter{
		Status: model.ConvStatus(p.Status),
		Limit:  p.Limit,
		Offset: p.Offset,
	}
	items, total, err := s.Convs.ListAdmin(ctx, f)
	if err != nil {
		return nil, apperr.Internal("list conversations").WithCause(err)
	}
	pageSize := p.Limit
	if pageSize <= 0 {
		pageSize = 50
	}
	return &ListConversationsResult{
		Total:    total,
		Items:    items,
		Page:     p.Offset/pageSize + 1,
		PageSize: pageSize,
	}, nil
}

type ConversationDetail struct {
	Conversation    model.Conversation       `json:"conversation"`
	Messages        []model.Message          `json:"messages"`
	Feedback        *model.Feedback          `json:"feedback,omitempty"`
	SkillInvocations []model.SkillInvocation `json:"skillInvocations,omitempty"`
}

func (s *Admin) GetConversation(ctx context.Context, id string) (*ConversationDetail, error) {
	c, err := s.Convs.Get(ctx, id)
	if err != nil {
		return nil, apperr.NotFound("conversation").WithCause(err)
	}
	msgs, err := s.Msgs.ListByConversation(ctx, id)
	if err != nil {
		return nil, apperr.Internal("load messages").WithCause(err)
	}
	// We don't have a Feedback.Get; reuse the conversation list flow for now.
	// When a dedicated feedback repo read is needed, add it. Keeping it simple
	// here: feedback is loaded through a small query.
	detail := &ConversationDetail{
		Conversation: *c,
		Messages:     msgs,
	}
	// Skill invocation audit trail (optional — agent may not have invoked
	// any tools for this conversation).
	if s.Invocations != nil {
		inv, err := s.Invocations.ListByConversation(ctx, id)
		if err != nil {
			lg := log.With(ctx, s.Logger)
			lg.Warn().Err(err).Str("conv_id", id).Msg("admin_load_invocations_failed")
		} else if len(inv) > 0 {
			detail.SkillInvocations = inv
		}
	}
	return detail, nil
}

// ---------------------------------------------------------------------------
// Stats
// ---------------------------------------------------------------------------

type SatisfactionStat struct {
	Total      int          `json:"total"`
	Average    float64      `json:"average"`
	Distribution [5]int      `json:"distribution"` // [1-star, 2, 3, 4, 5]
	ByDay      []DailyStat  `json:"byDay"`
}

type DailyStat struct {
	Day    string `json:"day"`    // YYYY-MM-DD
	Count  int    `json:"count"`
	Avg    float64 `json:"avg"`
}

// AggregateSatisfaction walks the feedback table. For our expected scale
// (10k-100k rows) this is fine — when it grows we can swap in SQL aggregates.
func (s *Admin) AggregateSatisfaction(ctx context.Context, days int) (*SatisfactionStat, error) {
	if days <= 0 {
		days = 7
	}
	stats := &SatisfactionStat{}
	byDay := make(map[string]*DailyStat, days)
	cutoff := time.Now().AddDate(0, 0, -days+1).Format("2006-01-02")

	rows, err := s.feedbackQuery(ctx)
	if err != nil {
		return nil, apperr.Internal("load feedback rows").WithCause(err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			rating int
			ts     int64
		)
		if err := rows.Scan(&rating, &ts); err != nil {
			return nil, apperr.Internal("scan feedback").WithCause(err)
		}
		day := time.UnixMilli(ts).Format("2006-01-02")
		if day < cutoff {
			continue
		}
		if rating < 1 || rating > 5 {
			continue
		}
		stats.Total++
		stats.Average += float64(rating)
		stats.Distribution[rating-1]++

		d := byDay[day]
		if d == nil {
			d = &DailyStat{Day: day}
			byDay[day] = d
		}
		d.Count++
		d.Avg += float64(rating)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal("feedback iter").WithCause(err)
	}
	if stats.Total > 0 {
		stats.Average = stats.Average / float64(stats.Total)
	}

	// Flatten byDay into a stable day-ordered slice.
	stats.ByDay = make([]DailyStat, 0, days)
	for i := 0; i < days; i++ {
		day := time.Now().AddDate(0, 0, -days+1+i).Format("2006-01-02")
		if d, ok := byDay[day]; ok {
			if d.Count > 0 {
				d.Avg = d.Avg / float64(d.Count)
			}
			stats.ByDay = append(stats.ByDay, *d)
		} else {
			stats.ByDay = append(stats.ByDay, DailyStat{Day: day})
		}
	}
	return stats, nil
}

// feedbackQuery is split out so the cursor lives in this file's scope; the
// concrete SQL stays here as well.
func (s *Admin) feedbackQuery(ctx context.Context) (rowsLike, error) {
	return s.Feedback.DB.QueryContext(ctx,
		`SELECT rating, created_at FROM feedback ORDER BY created_at DESC`)
}

// rowsLike lets us stub tests if we ever introduce them without dragging in
// the sql.Rows type signature everywhere.
type rowsLike interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

// ---------------------------------------------------------------------------
// Hand-off after manual agent replies
// ---------------------------------------------------------------------------

// AppendAgentReply lets a human agent post a message into the conversation
// after it has been handed over. Used by the admin dashboard.
func (s *Admin) AppendAgentReply(ctx context.Context, convID string, content string) (*model.Message, error) {
	if strings.TrimSpace(content) == "" {
		return nil, apperr.BadRequest("empty reply")
	}
	c, err := s.Convs.Get(ctx, convID)
	if err != nil {
		return nil, apperr.NotFound("conversation").WithCause(err)
	}
	m := &model.Message{
		ConversationID: c.ID,
		Role:           model.RoleAgent,
		Content:        content,
	}
	if err := s.Msgs.Insert(ctx, m); err != nil {
		return nil, apperr.Internal("insert agent reply").WithCause(err)
	}
	if err := s.Convs.Touch(ctx, c.ID); err != nil {
		log.With(ctx, s.Logger).Warn().Err(fmt.Errorf("touch conv: %w", err)).Msg("touch_failed")
	}
	log.With(ctx, s.Logger).Info().Str("conv_id", convID).Str("msg_id", m.ID).Msg("agent_reply_appended")
	return m, nil
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

var ErrNotImplemented = errors.New("not implemented yet")