package repo

import (
	"context"
	"database/sql"
	"time"
)

// Nonces is the storage backend for replay-protection. InsertNonce returns
// (true, nil) on success, (false, nil) if the nonce already exists (replay
// attempt), and (false, err) on a database failure.
type Nonces struct{ DB *sql.DB }

func NewNonces(db *sql.DB) *Nonces { return &Nonces{DB: db} }

// InsertOnce attempts to insert (nonce, expiresAt). The UNIQUE constraint
// on nonce makes it the atomic "have I seen this nonce before" check.
func (r *Nonces) InsertOnce(ctx context.Context, nonce string, expiresAt int64) (bool, error) {
	res, err := r.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO request_nonces(nonce, expires_at) VALUES(?, ?)`, nonce, expiresAt)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// PruneExpired drops nonces older than cutoff. Cheap; called periodically.
func (r *Nonces) PruneExpired(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.DB.ExecContext(ctx,
		`DELETE FROM request_nonces WHERE expires_at < ?`, cutoff.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
