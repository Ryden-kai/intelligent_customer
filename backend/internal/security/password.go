package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Password hashing uses Argon2id with parameters tuned for an interactive
// login (~200ms on a modern server). The hash is serialised in the standard
// PHC string format so the parameters travel with the value:
//
//	$argon2id$v=19$m=<memKB>,t=<iters>,p=<threads>$<saltB64>$<hashB64>
//
// Verifying extracts the parameters from the encoded string, so we can
// raise the work factor later and old hashes still work until users
// re-login (no migration script required).

const (
	argonTime    uint32 = 2         // iterations
	argonMemory  uint32 = 64 * 1024 // 64 MiB
	argonThreads uint8  = 4         // lanes
	argonKeyLen  uint32 = 32        // hash output size (bytes)
	argonSaltLen        = 16        // salt size (bytes)
)

var (
	ErrPasswordEmpty    = errors.New("password must not be empty")
	ErrHashMalformed    = errors.New("password hash is malformed")
	ErrAlgorithmUnknown = errors.New("password hash uses an unsupported algorithm")
)

// HashPassword returns an Argon2id-encoded hash of plain suitable for direct
// storage in admin_users.password_hash.
func HashPassword(plain string) (string, error) {
	if plain == "" {
		return "", ErrPasswordEmpty
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	hash := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	// PHC fields must use the standard alphabet — base64.RawStdEncoding
	// (no padding) — so we mirror it on both encode and decode sides.
	saltB64 := base64.RawStdEncoding.EncodeToString(salt)
	hashB64 := base64.RawStdEncoding.EncodeToString(hash)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, saltB64, hashB64,
	), nil
}

// VerifyPassword reports whether plain matches the encoded Argon2id hash.
// It is constant-time only with respect to the PHC field comparison; the
// argon2.IDKey call itself dominates timing.
func VerifyPassword(encoded, plain string) (bool, error) {
	if encoded == "" {
		return false, ErrHashMalformed
	}
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=...,t=...,p=...", "<salt>", "<hash>"]
	if len(parts) != 6 || parts[0] != "" {
		return false, ErrHashMalformed
	}
	if parts[1] != "argon2id" {
		return false, ErrAlgorithmUnknown
	}
	// parts[2] is the version. We accept any v=argon2.Version for forward
	// compatibility — the lib will reject mismatches internally if needed.
	var (
		memK    uint32
		iters   uint32
		threads uint8
	)
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memK, &iters, &threads); err != nil {
		return false, fmt.Errorf("%w: bad params %q", ErrHashMalformed, parts[3])
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("%w: bad salt", ErrHashMalformed)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("%w: bad hash", ErrHashMalformed)
	}

	got := argon2.IDKey([]byte(plain), salt, iters, memK, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}