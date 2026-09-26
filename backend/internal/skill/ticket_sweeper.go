package skill

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

// TicketSweeper periodically transitions stale skill_pending_tickets rows
// from "pending" to "expired". Without this background job a ticket left
// dangling for >5 minutes would never update its status — the confirm
// endpoint would still 409 (the row-level check covers it), but the
// front-end would never see an "expired" message until the user retried.
//
// PRD REQ-007: "未确认 ticket 必须 5 分钟内自动 expired。"
//
// Design notes:
//   - One ticker per process; intervals < 1 minute are accepted for tests.
//   - The first tick fires after `interval` (not at t=0) so a slow boot
//     doesn't race the migrations.
//   - The provided logger is shared — every sweep emits one structured
//     line so a metrics pipeline can count expired/minute.
type TicketSweeper struct {
	Tickets  *Tickets
	Interval time.Duration
	Logger   zerolog.Logger
}

// Run blocks until ctx is cancelled. Returns nil on graceful shutdown;
// the underlying SweepExpired errors are logged and swallowed (a single
// transient SQLite lock should not kill the loop).
func (s *TicketSweeper) Run(ctx context.Context) error {
	interval := s.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	lg := s.Logger.With().Str("component", "ticket_sweeper").Dur("interval", interval).Logger()
	lg.Info().Msg("ticket_sweeper_started")

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			lg.Info().Msg("ticket_sweeper_stopped")
			return nil
		case <-t.C:
			// Use a fresh short-lived context so a wedged DB can't pin
			// the sweeper forever. 5s is generous — UPDATE on the
			// (status, expires_at) index is O(matches) and SQLite WAL
			// rarely holds the writer lock for more than a few ms.
			sweepCtx, sCancel := context.WithTimeout(context.Background(), 5*time.Second)
			n, err := s.Tickets.SweepExpired(sweepCtx)
			sCancel()
			if err != nil {
				lg.Error().Err(err).Msg("ticket_sweeper_failed")
				continue
			}
			if n > 0 {
				lg.Info().Int("swept", n).Msg("ticket_sweeper_expired")
			} else {
				lg.Debug().Msg("ticket_sweeper_idle")
			}
		}
	}
}