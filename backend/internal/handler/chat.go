// Package handler translates HTTP-shaped calls into service calls. Handlers
// never implement business logic and never talk to repositories directly.
package handler

import (
	"net/http"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/service"
)

type Chat struct {
	Service *service.AgentChat
	Logger  zerolog.Logger
}

// ServeHTTP delegates straight to AgentChat.ServeHTTP, which writes the
// NDJSON stream and the final {type:done} event. The handler stays a
// thin shim so the router signature stays consistent.
func (h *Chat) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Service.ServeHTTP(w, r)
}