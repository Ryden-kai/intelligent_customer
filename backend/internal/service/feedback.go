package service

import (
	"context"
	"strings"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/model"
	"intelligent_customer/backend/internal/repo"
)

type Feedback struct {
	Convs    *repo.Conversations
	Feedback *repo.Feedback
	Msgs     *repo.Messages
	JEV      *jev.Client
	Logger   zerolog.Logger
}

type FeedbackInput struct {
	ConversationID string `json:"conversationId"`
	Rating         int    `json:"rating"`
	Comment        string `json:"comment"`
}

// Submit records user satisfaction and (when comment is non-empty) lets Jev
// infer a sentiment score that is kept internally for future model fine-tune.
func (s *Feedback) Submit(ctx context.Context, in FeedbackInput) (*model.Feedback, error) {
	if in.Rating < 1 || in.Rating > 5 {
		return nil, apperr.BadRequest("rating must be between 1 and 5")
	}
	if in.ConversationID == "" {
		return nil, apperr.BadRequest("conversationId required")
	}
	c, err := s.Convs.Get(ctx, in.ConversationID)
	if err != nil {
		return nil, apperr.NotFound("conversation").WithCause(err)
	}
	comment := strings.TrimSpace(in.Comment)
	if comment == "" && s.JEV.Enabled() {
		// Jev still useful for plain rating corroboration: not in this release.
	}
	f := &model.Feedback{
		ConversationID: c.ID,
		Rating:         in.Rating,
		Comment:        comment,
	}
	if err := s.Feedback.Upsert(ctx, f); err != nil {
		log.With(ctx, s.Logger).Error().Err(err).Str("conv_id", c.ID).Msg("save_feedback_failed")
		return nil, apperr.Internal("save feedback").WithCause(err)
	}
	log.With(ctx, s.Logger).Info().Str("conv_id", c.ID).Int("rating", f.Rating).Msg("feedback_saved")
	return f, nil
}