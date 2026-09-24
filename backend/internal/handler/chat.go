// Package handler translates HTTP-shaped calls into service calls. Handlers
// never implement business logic and never talk to repositories directly.
package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/apperr"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/service"
)

type Chat struct {
	Service *service.Chat
	Logger  zerolog.Logger
}

type chatRequest struct {
	ConversationID string `json:"conversationId"`
	UserID         string `json:"userId"`
	Content        string `json:"content"`
}

func (h *Chat) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "chat").Logger()

	var body chatRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		lg.Warn().Err(err).Msg("chat_decode_failed")
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}

	resp, err := h.Service.Handle(r.Context(), service.ChatRequest{
		ConversationID: body.ConversationID,
		UserID:         body.UserID,
		Content:        body.Content,
	})
	if err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			lg.Warn().
				Err(err).
				Str("code", string(ae.Code)).
				Int("http", ae.HTTPStatus).
				Msg("chat_service_warn")
		} else {
			lg.Error().Err(err).Msg("chat_service_error")
		}
		writeAppError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	lg.Info().
		Str("conv_id", resp.ConversationID).
		Str("intent", string(resp.Intent)).
		Str("source", resp.Source).
		Bool("handed_over", resp.HandedOver).
		Msg("chat_response_sent")
	_ = json.NewEncoder(w).Encode(resp)
}