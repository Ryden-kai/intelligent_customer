// Package security provides the application-layer protections the API uses:
//   - HMAC-SHA256 request signing with timestamp + nonce anti-replay
//   - AES-256-GCM symmetric encryption of sensitive fields
//
// Wire format (HTTP request):
//
//	X-Timestamp : <unix milliseconds>
//	X-Nonce     : <UUIDv4 hex without dashes>
//	X-Signature : <hex of HMAC-SHA256(secret, canonical)>
//
// Canonical string built from the request:
//
//	<method>\n<path>\n<timestamp>\n<nonce>\n<sha256-hex(body)>
//
// On the server side middleware.VerifySignature enforces: timestamp drift
// within ±TTL, nonce uniqueness (cached via repo), and HMAC equality.
package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderTimestamp = "X-Timestamp"
	HeaderNonce     = "X-Nonce"
	HeaderSignature = "X-Signature"

	// MaxClockSkew is the maximum allowed drift between client and server
	// timestamps. Requests outside this window are rejected.
	DefaultMaxClockSkew = 5 * time.Minute

	// MinNonceLen keeps callers from registering trivially-short strings
	// as nonces. UUIDv4 hex is 32 chars so 24 is a sane lower bound.
	MinNonceLen = 24
)

// CanonicalString builds the deterministic string the HMAC is computed over.
// It is exported so callers (typically the frontend) can reproduce the
// signature byte-for-byte.
func CanonicalString(method, path string, ts int64, nonce string, body []byte) string {
	bodyHash := sha256.Sum256(body)
	return strings.Join([]string{
		strings.ToUpper(method),
		path,
		strconv.FormatInt(ts, 10),
		nonce,
		hex.EncodeToString(bodyHash[:]),
	}, "\n")
}

// Sign returns the hex-encoded HMAC-SHA256 of the canonical request.
func Sign(secret []byte, method, path string, ts int64, nonce string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(CanonicalString(method, path, ts, nonce, body)))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify checks that sigHex matches what the server would compute for the
// given inputs and returns the timestamp + nonce on success so the caller
// can persist the nonce and record the canonical request time.
func Verify(secret []byte, method, path string, ts int64, nonce, sigHex string, body []byte, now time.Time, skew time.Duration) (err error) {
	if skew <= 0 {
		skew = DefaultMaxClockSkew
	}
	tsTime := time.UnixMilli(ts)
	if delta := now.Sub(tsTime); delta > skew || delta < -skew {
		return fmt.Errorf("timestamp out of window: delta=%s", delta)
	}
	if len(nonce) < MinNonceLen {
		return errors.New("nonce too short")
	}
	got, err := hex.DecodeString(sigHex)
	if err != nil {
		return fmt.Errorf("signature not hex: %w", err)
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(CanonicalString(method, path, ts, nonce, body)))
	if !hmac.Equal(got, mac.Sum(nil)) {
		return errors.New("signature mismatch")
	}
	return nil
}

// RandomNonce returns a fresh hex nonce (no dashes). It panics only on the
// extremely unlikely case the system RNG fails — there is no recovery path.
func RandomNonce() string {
	var buf [16]byte
	if _, err := io.ReadFull(rand.Reader, buf[:]); err != nil {
		panic("security: failed to read random bytes: " + err.Error())
	}
	return hex.EncodeToString(buf[:])
}

// CurrentMilli returns the current unix-millisecond timestamp. Exported so
// callers in tests can stub the clock consistently.
func CurrentMilli() int64 { return time.Now().UnixMilli() }

// EqualMAC is a constant-time hex comparison helper kept here so the rest
// of the codebase doesn't reach into crypto/hmac directly.
func EqualMAC(a, b []byte) bool { return hmac.Equal(a, b) }

// hashExists is a build-time check that we're using the right hash import;
// in case someone later swaps the algorithm.
var _ hash.Hash = sha256.New()

// requestFields is just a typed wrapper so middleware can reach into a
// canonical set of header names without typo risk.
type requestFields struct {
	ts   int64
	nonce string
	sig  string
}

func readHeaders(r *http.Request) (requestFields, error) {
	tsStr := strings.TrimSpace(r.Header.Get(HeaderTimestamp))
	if tsStr == "" {
		return requestFields{}, errors.New("missing X-Timestamp")
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return requestFields{}, fmt.Errorf("invalid X-Timestamp: %w", err)
	}
	nonce := strings.TrimSpace(r.Header.Get(HeaderNonce))
	if nonce == "" {
		return requestFields{}, errors.New("missing X-Nonce")
	}
	sig := strings.TrimSpace(r.Header.Get(HeaderSignature))
	if sig == "" {
		return requestFields{}, errors.New("missing X-Signature")
	}
	return requestFields{ts: ts, nonce: nonce, sig: sig}, nil
}
