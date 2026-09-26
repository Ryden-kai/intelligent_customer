// Package config centralises environment-variable loading and validation.
// All configuration is read from .env (via godotenv) and process env.
// We fail fast on startup so misconfiguration never reaches a request.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type LLMProvider string

const (
	LLMProviderOpenAI  LLMProvider = "openai"
	LLMProviderMiniMax LLMProvider = "MiniMax"
)

type Config struct {
	ServerAddr        string
	ServerReadTimeout time.Duration
	ServerWriteTimeout time.Duration

	LogLevel  string
	LogPretty bool

	DBPath string

	JWTSecret string
	JWTTTL    time.Duration

	AdminUsername string
	AdminPassword string

	LLMProvider LLMProvider
	OpenAI      LLMChannel
	MiniMax     LLMChannel

	JEVBaseURL string
	JEVModel   string
	JEVAPIKey  string

	HandoverConfidenceThreshold   float64
	MsgHandoverCountThreshold     int

	CORSOrigins []string

	// TLS — when both files exist, the server runs ListenAndServeTLS instead
	// of plain HTTP. Leaving either empty falls back to HTTP for dev.
	TLSCertFile string
	TLSKeyFile  string

	// App-layer security.
	SignatureRequired        bool
	SignatureClockSkew       time.Duration
	SignatureNonceTTL        time.Duration
	EncryptSensitiveFields   bool
	EncryptionKeyExplicitHex string // optional override; otherwise HKDF(JWT_SECRET)

	// Agent loop tuning.
	AgentMaxSteps      int
	AgentMaxTokens     int
	AgentMaxWallclock  time.Duration
	AgentSkillTimeout  time.Duration
	AgentSystemPrompt  string

	// Skill registry.
	SkillDemoUserID    string

	// Ticket sweeper (background job that expires stale confirm/cancel
	// tickets after their TTL). Set to 0 to disable.
	TicketSweepInterval time.Duration

	// AppEnv — "production" / "development" / 其他。
	// 生产环境启用错误脱敏（middleware.Sanitize）；开发环境保留 stack。
	// 默认 "development"。
	AppEnv string
}

type LLMChannel struct {
	BaseURL  string
	Model    string
	APIKey   string
	Provider string // informational label, set by New()
}

func Load() (*Config, error) {
	// .env is optional; production injects env vars directly.
	_ = godotenv.Load()

	cfg := &Config{
		ServerAddr:        getStr("SERVER_ADDR", ":8080"),
		ServerReadTimeout: getDur("SERVER_READ_TIMEOUT", 30*time.Second),
		ServerWriteTimeout: getDur("SERVER_WRITE_TIMEOUT", 30*time.Second),

		LogLevel:  getStr("LOG_LEVEL", "debug"),
		LogPretty: getBool("LOG_PRETTY", true),

		DBPath: getStr("DB_PATH", "./data/intelligent_customer.db"),

		JWTSecret: getStr("JWT_SECRET", ""),
		JWTTTL:    time.Duration(getInt("JWT_TTL_HOURS", 24)) * time.Hour,

		AdminUsername: getStr("ADMIN_USERNAME", "admin"),
		AdminPassword: getStr("ADMIN_PASSWORD", "admin123"),

		LLMProvider: LLMProvider(getStr("LLM_PROVIDER", "MiniMax")),

		OpenAI: LLMChannel{
			BaseURL: getStr("OPENAI_BASE_URL", "https://api.openai.com/v1"),
			Model:   getStr("OPENAI_MODEL", "gpt-4o-mini"),
			APIKey:  getStr("OPENAI_API_KEY", ""),
		},
		MiniMax: LLMChannel{
			// minimax cn exposes an Anthropic-compatible endpoint at
			// https://api.minimax.cn/anthropic (per the platform's
			// 快速接入 docs). The URL ends in /anthropic so requests land
			// on /v1/messages. NOTE: avoid the older api.minimaxi.cn
			// host — it points to a defunct international region.
			BaseURL: getStr("MiniMax_BASE_URL", "https://api.minimax.cn/anthropic"),
			Model:   getStr("MiniMax_MODEL", "MiniMax-M3"),
			APIKey:  getStr("MiniMax_API_KEY", ""),
		},

		JEVBaseURL: getStr("JEV_BASE_URL", ""),
		JEVModel:   getStr("JEV_MODEL", "typesafe/jev-1.13"),
		JEVAPIKey:  getStr("JEV_API_KEY", ""),

		HandoverConfidenceThreshold: getFloat("HANDOVER_CONFIDENCE_THRESHOLD", 0.55),
		MsgHandoverCountThreshold:   getInt("MSG_HANDOVER_COUNT_THRESHOLD", 3),

		CORSOrigins: splitCSV(getStr("CORS_ORIGINS", "http://localhost:5173,http://localhost:8080,https://localhost:8443,https://*.pages.dev")),

		TLSCertFile: getStr("TLS_CERT_FILE", ""),
		TLSKeyFile:  getStr("TLS_KEY_FILE", ""),

		SignatureRequired:      getBool("SIGNATURE_REQUIRED", true),
		SignatureClockSkew:     getDur("SIGNATURE_CLOCK_SKEW", 5*time.Minute),
		SignatureNonceTTL:      getDur("SIGNATURE_NONCE_TTL", 10*time.Minute),
		EncryptSensitiveFields: getBool("ENCRYPT_SENSITIVE_FIELDS", true),
		EncryptionKeyExplicitHex: getStr("ENCRYPTION_KEY_HEX", ""),

		AgentMaxSteps:     getInt("AGENT_MAX_STEPS", 5),
		AgentMaxTokens:    getInt("AGENT_MAX_TOKENS", 4000),
		AgentMaxWallclock: getDur("AGENT_MAX_WALLCLOCK", 20*time.Second),
		AgentSkillTimeout: getDur("AGENT_SKILL_TIMEOUT", 5*time.Second),
		AgentSystemPrompt: getStr("AGENT_SYSTEM_PROMPT", ""),

		SkillDemoUserID: getStr("SKILL_DEMO_USER_ID", "demo-user"),

		TicketSweepInterval: getDur("TICKET_SWEEP_INTERVAL", time.Minute),

		AppEnv: getStr("APP_ENV", "development"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if len(c.JWTSecret) < 16 {
		return errors.New("JWT_SECRET must be at least 16 characters")
	}
	// Normalise provider to the canonical form so logs and downstream
	// branching are stable regardless of how the user typed the env value.
	switch strings.ToLower(string(c.LLMProvider)) {
	case "openai":
		c.LLMProvider = LLMProviderOpenAI
		if c.OpenAI.APIKey == "" {
			return errors.New("OPENAI_API_KEY is required when LLM_PROVIDER=openai")
		}
	case "minimax":
		c.LLMProvider = LLMProviderMiniMax
		if c.MiniMax.APIKey == "" {
			return errors.New("MiniMax_API_KEY is required when LLM_PROVIDER=MiniMax")
		}
		// Defensive check: the older api.minimaxi.cn host (with the extra
		// 'i') points to a defunct international region — requests resolve
		// and then EOF mid-handshake, which is hard to diagnose from logs.
		// Reject early with a clear message so operators see the fix path
		// immediately instead of debugging a "all channels failed" error
		// at request time.
		if strings.Contains(c.MiniMax.BaseURL, "minimaxi.cn") {
			return fmt.Errorf(
				"MiniMax_BASE_URL %q uses the defunct api.minimaxi.cn host. "+
					"Use https://api.minimax.cn/anthropic instead "+
					"(see backend/.env.example lines 43-46)",
				c.MiniMax.BaseURL,
			)
		}
	default:
		return fmt.Errorf("invalid LLM_PROVIDER %q (want openai | MiniMax)", c.LLMProvider)
	}
	if c.HandoverConfidenceThreshold <= 0 || c.HandoverConfidenceThreshold > 1 {
		return errors.New("HANDOVER_CONFIDENCE_THRESHOLD must be in (0,1]")
	}
	return nil
}

// ActiveLLM returns the channel selected by LLM_PROVIDER.
func (c *Config) ActiveLLM() LLMChannel {
	switch c.LLMProvider {
	case LLMProviderOpenAI:
		return c.OpenAI
	default:
		return c.MiniMax
	}
}

func getStr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getBool(k string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func getInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getFloat(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return def
}

func getDur(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}