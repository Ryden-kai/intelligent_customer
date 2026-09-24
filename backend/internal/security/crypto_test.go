package security

import (
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := DeriveKey([]byte("test-secret-please-replace-with-real-32-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	plain := "用户消息：我想退款，怎么办？"
	ct, err := EncryptString(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if ct == plain {
		t.Fatalf("ciphertext equals plaintext")
	}
	if !strings.HasSuffix(ct, "=") && !strings.Contains(ct, "=") {
		t.Fatalf("expected base64 output")
	}
	got, err := DecryptString(key, ct)
	if err != nil {
		t.Fatal(err)
	}
	if got != plain {
		t.Fatalf("roundtrip mismatch: got %q want %q", got, plain)
	}
}

func TestEncryptEmptyShortCircuits(t *testing.T) {
	key, _ := DeriveKey([]byte("test-secret-please-replace-with-real-32-bytes"))
	ct, err := EncryptString(key, "")
	if err != nil {
		t.Fatal(err)
	}
	if ct != "" {
		t.Fatalf("empty plaintext should encrypt to empty: %q", ct)
	}
	pt, err := DecryptString(key, "")
	if err != nil {
		t.Fatal(err)
	}
	if pt != "" {
		t.Fatalf("empty ciphertext should decrypt to nil: %q", pt)
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	key, _ := DeriveKey([]byte("test-secret-please-replace-with-real-32-bytes"))
	ct, err := EncryptString(key, "hello world")
	if err != nil {
		t.Fatal(err)
	}
	// Flip one char in the middle.
	bad := []byte(ct)
	if bad[5] == 'A' {
		bad[5] = 'B'
	} else {
		bad[5] = 'A'
	}
	if _, err := DecryptString(key, string(bad)); err == nil {
		t.Fatalf("expected decryption error on tampered ciphertext")
	}
}

func TestDeriveKeyRejectsShortSecret(t *testing.T) {
	if _, err := DeriveKey([]byte("short")); err == nil {
		t.Fatalf("expected error on short secret")
	}
}

func TestDecryptRejectsShortInput(t *testing.T) {
	key, _ := DeriveKey([]byte("test-secret-please-replace-with-real-32-bytes"))
	if _, err := DecryptString(key, "abc"); err == nil {
		t.Fatalf("expected error on truncated ciphertext")
	}
}
