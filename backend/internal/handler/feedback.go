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

type Feedback struct {
	Service *service.Feedback
	Logger  zerolog.Logger
}

func (h *Feedback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	lg := log.With(r.Context(), h.Logger).With().Str("endpoint", "feedback").Logger()
	var body service.FeedbackInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		lg.Warn().Err(err).Msg("feedback_decode_failed")
		writeAppError(w, r, apperr.BadRequest("invalid json body").WithCause(err))
		return
	}
	f, err := h.Service.Submit(r.Context(), body)
	if err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			lg.Warn().
				Err(err).
				Str("code", string(ae.Code)).
				Int("http", ae.HTTPStatus).
				Msg("feedback_service_warn")
		} else {
			lg.Error().Err(err).Msg("feedback_service_error")
		}
		writeAppError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	lg.Info().Str("conv_id", f.ConversationID).Int("rating", f.Rating).Msg("feedback_ok")
	_ = json.NewEncoder(w).Encode(f)
}