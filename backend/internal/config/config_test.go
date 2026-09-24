package config

import (
	"strings"
	"testing"
)

func TestLoadSuccess(t *testing.T) {
	t.Setenv("JWT_SECRET", "this-is-a-32-byte-development-secret-1234")
	t.Setenv("LLM_PROVIDER", "minimax")
	t.Setenv("MiniMax_API_KEY", "sk-test")
	t.Setenv("JEV_BASE_URL", "")
	t.Setenv("JEV_API_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLMProvider != LLMProviderMiniMax {
		t.Fatalf("provider not normalised, got %q", cfg.LLMProvider)
	}
	if cfg.JWTSecret == "" {
		t.Fatalf("jwt secret empty")
	}
}

func TestLoadRejectsBadProvider(t *testing.T) {
	t.Setenv("JWT_SECRET", "this-is-a-32-byte-development-secret-1234")
	t.Setenv("LLM_PROVIDER", "claude")
	t.Setenv("OPENAI_API_KEY", "sk-x")
	if _, err := Load(); err == nil {
		t.Fatalf("expected error for bad provider")
	}
}

func TestLoadRequiresJWT(t *testing.T) {
	t.Setenv("JWT_SECRET", "short")
	t.Setenv("LLM_PROVIDER", "MiniMax")
	t.Setenv("MiniMax_API_KEY", "sk-x")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("expected JWT_SECRET error, got %v", err)
	}
}

func TestLoadRequiresAPIKey(t *testing.T) {
	t.Setenv("JWT_SECRET", "this-is-a-32-byte-development-secret-1234")
	t.Setenv("LLM_PROVIDER", "MiniMax")
	t.Setenv("MiniMax_API_KEY", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MiniMax_API_KEY") {
		t.Fatalf("expected api key error, got %v", err)
	}
}

func TestGetBoolDefaults(t *testing.T) {
	t.Setenv("LOG_PRETTY", "")
	if v := getBool("LOG_PRETTY", true); !v {
		t.Fatalf("expected true default")
	}
	t.Setenv("LOG_PRETTY", "false")
	if v := getBool("LOG_PRETTY", true); v {
		t.Fatalf("expected false")
	}
	t.Setenv("LOG_PRETTY", "yes")
	if v := getBool("LOG_PRETTY", false); !v {
		t.Fatalf("expected yes → true")
	}
}