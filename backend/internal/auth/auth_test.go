package auth

import (
	"testing"
	"time"
)

func TestSignParseRoundTrip(t *testing.T) {
	iss := NewIssuer("test-secret-please-replace-with-real-32-bytes", time.Hour)
	tok, err := iss.Sign("alice", "admin")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if tok == "" {
		t.Fatalf("empty token")
	}
	c, err := iss.Parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Username != "alice" || c.Role != "admin" {
		t.Fatalf("claims wrong: %+v", c)
	}
}

func TestParseRejectsTampered(t *testing.T) {
	iss := NewIssuer("test-secret-please-replace-with-real-32-bytes", time.Hour)
	tok, err := iss.Sign("alice", "admin")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// Flip one character of the signature to force rejection.
	bad := tok[:len(tok)-2] + "XX"
	if _, err := iss.Parse(bad); err == nil {
		t.Fatalf("expected error on tampered token")
	}
}

func TestParseRejectsWrongSecret(t *testing.T) {
	a := NewIssuer("test-secret-please-replace-with-real-32-bytes", time.Hour)
	b := NewIssuer("completely-different-secret-still-32+bytes-long", time.Hour)
	tok, _ := a.Sign("alice", "admin")
	if _, err := b.Parse(tok); err == nil {
		t.Fatalf("expected error with wrong secret")
	}
}

func TestParseRejectsExpired(t *testing.T) {
	iss := NewIssuer("test-secret-please-replace-with-real-32-bytes", -time.Second)
	tok, _ := iss.Sign("alice", "admin")
	if _, err := iss.Parse(tok); err == nil {
		t.Fatalf("expected error on expired token")
	}
}