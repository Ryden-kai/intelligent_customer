// Package main wires every layer of the API service together. The frontend
// lives in a separate repo (../frontend) and is deployed independently
// (e.g. Cloudflare Pages). This binary is API-only.
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/config"
	"intelligent_customer/backend/internal/db"
	"intelligent_customer/backend/internal/handler"
	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/llm"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/middleware"
	"intelligent_customer/backend/internal/repo"
	"intelligent_customer/backend/internal/security"
	"intelligent_customer/backend/internal/seed"
	"intelligent_customer/backend/internal/server"
	"intelligent_customer/backend/internal/service"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := log.New(cfg.LogLevel, cfg.LogPretty)
	logger.Info().
		Str("addr", cfg.ServerAddr).
		Str("llm_provider", string(cfg.LLMProvider)).
		Str("jev_enabled", boolStr(cfg.JEVBaseURL != "" && cfg.JEVAPIKey != "")).
		Bool("signature_required", cfg.SignatureRequired).
		Bool("encrypt_sensitive", cfg.EncryptSensitiveFields).
		Msg("boot_start")

	// ---- Database ----------------------------------------------------------
	conn, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer conn.Close()
	logger.Info().Str("path", cfg.DBPath).Msg("db_open_ok")

	if err := db.Migrate(conn, db.MigrationsFS, "migrations"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	logger.Info().Msg("db_migrate_ok")

	res, err := seed.Run(context.Background(), conn, seed.Bootstrap{
		AdminUsername: cfg.AdminUsername,
		AdminPassword: cfg.AdminPassword,
	})
	if err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	if res.FAQsInserted > 0 {
		logger.Info().Int("seeded", res.FAQsInserted).Msg("seed_faqs_inserted")
	}
	if res.AdminCreated {
		logger.Warn().
			Str("username", cfg.AdminUsername).
			Msg("seed_admin_created_bootstrap_change_password_via_adminctl")
	}

	// ---- Logger alias with component tag -----------------------------------
	httpLogger := logger.With().Str("component", "http").Logger()
	svcLogger := logger.With().Str("component", "service").Logger()
	jevLogger := logger.With().Str("component", "jev").Logger()
	llmLogger := logger.With().Str("component", "llm").Logger()
	authLogger := logger.With().Str("component", "auth").Logger()

	// ---- Upstream clients -------------------------------------------------
	var llmClient llm.ChatCompleter
	switch cfg.LLMProvider {
	case config.LLMProviderMiniMax:
		// MiniMax cn exposes an Anthropic-compatible endpoint.
		llmClient = llm.NewAnthropic(llm.AnthropicChannel{
			BaseURL: cfg.MiniMax.BaseURL,
			Model:   cfg.MiniMax.Model,
			APIKey:  cfg.MiniMax.APIKey,
			Label:   string(cfg.LLMProvider),
		}, llmLogger)
	default:
		channel := cfg.ActiveLLM()
		llmClient = llm.NewOpenAI(llm.OpenAIChannel{
			BaseURL: channel.BaseURL,
			Model:   channel.Model,
			APIKey:  channel.APIKey,
			Label:   string(cfg.LLMProvider),
		}, llmLogger)
	}
	jevClient := jev.New(*cfg, jevLogger)

	logger.Info().
		Str("provider", llmClient.Identity().Provider).
		Str("model", llmClient.Identity().Name).
		Bool("jev", jevClient.Enabled()).
		Msg("clients_initialised")

	// ---- Encryption key (AES-256-GCM) -----------------------------------
	var encKey []byte
	if cfg.EncryptSensitiveFields {
		if cfg.EncryptionKeyExplicitHex != "" {
			raw, err := hex.DecodeString(cfg.EncryptionKeyExplicitHex)
			if err != nil || len(raw) != 32 {
				return fmt.Errorf("ENCRYPTION_KEY_HEX must be 64 hex chars (32 bytes): %w", err)
			}
			encKey = raw
		} else {
			encKey, err = security.DeriveKey([]byte(cfg.JWTSecret))
			if err != nil {
				return fmt.Errorf("derive encryption key: %w", err)
			}
		}
	}

	// ---- Repositories -----------------------------------------------------
	convs := repo.NewConversations(conn)
	msgs := repo.NewMessages(conn).WithEncryption(encKey)
	feedbacks := repo.NewFeedback(conn).WithEncryption(encKey)
	faqs := repo.NewFAQs(conn)
	signals := repo.NewHandoverSignals(conn)
	nonces := repo.NewNonces(conn)
	admins := repo.NewAdminUsers(conn)

	// ---- Services ---------------------------------------------------------
	chatSvc := &service.Chat{
		Convs:     convs,
		Msgs:      msgs,
		Feedbacks: feedbacks,
		FAQs:      faqs,
		Signals:   signals,
		LLM:       llmClient,
		JEV:       jevClient,
		HandoverCfg: service.HandoverConfig{
			ConfidenceThreshold: cfg.HandoverConfidenceThreshold,
			SignalCountLimit:    cfg.MsgHandoverCountThreshold,
		},
		Logger: svcLogger,
	}
	adminSvc := &service.Admin{
		Convs:    convs,
		Msgs:     msgs,
		Feedback: feedbacks,
		Logger:   svcLogger,
	}
	feedbackSvc := &service.Feedback{
		Convs:    convs,
		Feedback: feedbacks,
		Msgs:     msgs,
		JEV:      jevClient,
		Logger:   svcLogger,
	}

	// ---- Auth -------------------------------------------------------------
	issuer := auth.NewIssuer(cfg.JWTSecret, cfg.JWTTTL)
	authH := &handler.AuthHandlers{
		Issuer: issuer,
		Admins: admins,
		Logger: authLogger,
	}

	// ---- HTTP handlers ----------------------------------------------------
	chatH := &handler.Chat{Service: chatSvc, Logger: httpLogger}
	feedbackH := &handler.Feedback{Service: feedbackSvc, Logger: httpLogger}
	adminH := &handler.Admin{
		Service: adminSvc,
		Logger:  httpLogger,
		Auth:    authH,
		Issuer:  issuer,
	}

	// ---- Frontend note ----------------------------------------------------
	// This binary is API-only. The SPA lives at ../frontend and is deployed
	// independently (e.g. Cloudflare Pages). CORS in .env must allow that
	// origin or admin/chat calls will be blocked in the browser.

	// ---- Router -----------------------------------------------------------
	router := server.New(server.Deps{
		Logger:          httpLogger,
		CORSOrigins:     cfg.CORSOrigins,
		Issuer:          issuer,
		ChatHandler:     chatH,
		FeedbackHandler: feedbackH,
		AdminHandler:    adminH,
		AuthHandlers:    authH,
		SignatureOpts: &middleware.SignatureOptions{
			Secret:    []byte(cfg.JWTSecret),
			Nonces:    nonces,
			Logger:    httpLogger,
			ClockSkew: cfg.SignatureClockSkew,
			NonceTTL:  cfg.SignatureNonceTTL,
			Required:  cfg.SignatureRequired,
		},
	})

	// ---- Run with graceful shutdown ---------------------------------------
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := server.ListenAndServe(ctx, server.ListenOptions{
		Addr:        cfg.ServerAddr,
		ReadTO:      cfg.ServerReadTimeout,
		WriteTO:     cfg.ServerWriteTimeout,
		TLSCertFile: cfg.TLSCertFile,
		TLSKeyFile:  cfg.TLSKeyFile,
	}, router, httpLogger); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	logger.Info().Msg("bye")
	return nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}