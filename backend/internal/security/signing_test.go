package security

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	secret := []byte("test-secret-please-replace-with-real-32-bytes-or-more")
	body := []byte(`{"hello":"world"}`)
	ts := time.Now().UnixMilli()
	nonce := RandomNonce()

	sig := Sign(secret, "POST", "/api/chat?foo=bar", ts, nonce, body)
	if !strings.HasPrefix(sig, "ab") && len(sig) != 64 {
		// Sanity: HMAC-SHA256 hex is 64 chars.
		t.Fatalf("unexpected sig format: %q (len=%d)", sig, len(sig))
	}
	if err := Verify(secret, "POST", "/api/chat?foo=bar", ts, nonce, sig, body, time.Now(), time.Minute); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyRejectsTamperedBody(t *testing.T) {
	secret := []byte("test-secret-please-replace-with-real-32-bytes-or-more")
	ts := time.Now().UnixMilli()
	nonce := RandomNonce()
	body := []byte(`{"hello":"world"}`)
	sig := Sign(secret, "POST", "/api/x", ts, nonce, body)
	err := Verify(secret, "POST", "/api/x", ts, nonce, sig, []byte(`{"hello":"WORLD"}`), time.Now(), time.Minute)
	if err == nil || !strings.Contains(err.Error(), "signature mismatch") {
		t.Fatalf("expected signature mismatch, got %v", err)
	}
}

func TestVerifyRejectsExpiredTimestamp(t *testing.T) {
	secret := []byte("test-secret-please-replace-with-real-32-bytes-or-more")
	ts := time.Now().Add(-10 * time.Minute).UnixMilli()
	nonce := RandomNonce()
	body := []byte(`x`)
	sig := Sign(secret, "POST", "/a", ts, nonce, body)
	err := Verify(secret, "POST", "/a", ts, nonce, sig, body, time.Now(), time.Minute)
	if err == nil || !strings.Contains(err.Error(), "timestamp") {
		t.Fatalf("expected timestamp error, got %v", err)
	}
}

func TestVerifyRejectsBadHex(t *testing.T) {
	secret := []byte("test-secret-please-replace-with-real-32-bytes-or-more")
	ts := time.Now().UnixMilli()
	err := Verify(secret, "POST", "/a", ts, RandomNonce(), "ZZZZ", nil, time.Now(), time.Minute)
	if err == nil || !strings.Contains(err.Error(), "hex") {
		t.Fatalf("expected hex error, got %v", err)
	}
}

func TestVerifyRejectsShortNonce(t *testing.T) {
	secret := []byte("test-secret-please-replace-with-real-32-bytes-or-more")
	ts := time.Now().UnixMilli()
	err := Verify(secret, "POST", "/a", ts, "abc", "deadbeef", nil, time.Now(), time.Minute)
	if err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("expected nonce error, got %v", err)
	}
}

func TestCanonicalStringIsCaseInsensitiveOnMethod(t *testing.T) {
	secret := []byte("test-secret-please-replace-with-real-32-bytes-or-more")
	ts := time.Now().UnixMilli()
	nonce := RandomNonce()
	body := []byte("body")
	// CanonicalString upper-cases the method internally, so both Sign
	// invocations produce the same digest.
	a := Sign(secret, "POST", "/a", ts, nonce, body)
	b := Sign(secret, "post", "/a", ts, nonce, body)
	if a != b {
		t.Fatalf("canonical should normalise method case: %s vs %s", a, b)
	}
	if err := Verify(secret, "post", "/a", ts, nonce, a, body, time.Now(), time.Minute); err != nil {
		t.Fatalf("verify should accept mixed-case method: %v", err)
	}
}

func TestRandomNonceShape(t *testing.T) {
	n1 := RandomNonce()
	n2 := RandomNonce()
	if len(n1) != 32 {
		t.Fatalf("nonce length: %d", len(n1))
	}
	if n1 == n2 {
		t.Fatalf("nonces must be unique")
	}
	// must be hex
	if _, err := hex.DecodeString(n1); err != nil {
		t.Fatalf("not hex: %v", err)
	}
}
