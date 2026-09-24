package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// Application-layer encryption uses AES-256-GCM. The key is derived from
// JWT_SECRET via HKDF-SHA256 so the same secret produces both the JWT
// signing key and the encryption key without us storing two values in env.
//
// Wire format for an encrypted blob:
//
//	base64( nonce(12) || ciphertext || gcm_tag(16) )
//
// The 12-byte nonce is randomly generated per call (recommended by NIST
// SP 800-38D for GCM).

// InfoEncrypt is the HKDF info string distinguishing the encryption key
// from any other use of the master secret.
const InfoEncrypt = "intelligent_customer/app-encryption/v1"

// DeriveKey returns a 32-byte AES-256 key derived from a master secret.
func DeriveKey(master []byte) ([]byte, error) {
	if len(master) < 16 {
		return nil, errors.New("master secret must be >= 16 bytes")
	}
	r := hkdf.New(sha256.New, master, nil, []byte(InfoEncrypt))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("hkdf read: %w", err)
	}
	return key, nil
}

// Encrypt encrypts plaintext with AES-256-GCM and returns a base64 string.
// An empty plaintext returns an empty string (no work, no ciphertext).
func Encrypt(key []byte, plaintext []byte) (string, error) {
	if len(plaintext) == 0 {
		return "", nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("cipher.NewGCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("read nonce: %w", err)
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, len(nonce)+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt is the inverse of Encrypt. Empty input returns empty output so
// callers don't need a guard for nullable columns.
func Decrypt(key []byte, encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("base64: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("gcm.Open: %w", err)
	}
	return pt, nil
}

// EncryptString is a convenience wrapper for plaintext strings.
func EncryptString(key []byte, plaintext string) (string, error) {
	return Encrypt(key, []byte(plaintext))
}

// DecryptString is the inverse of EncryptString.
func DecryptString(key []byte, encoded string) (string, error) {
	b, err := Decrypt(key, encoded)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
