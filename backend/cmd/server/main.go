// Package main wires every layer of the API service together. The frontend
// lives in a separate repo (../frontend) and is deployed independently
// (e.g. Cloudflare Pages). This binary is API-only.
//
// Layer order, top-down:
//   - config + logging
//   - database + migrations + seed
//   - upstream clients (LLM, Jev)
//   - encryption key
//   - repositories
//   - skill registry (built-in + dynamic)
//   - services
//   - HTTP handlers
//   - router + middleware
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/agent"
	"intelligent_customer/backend/internal/audit"
	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/config"
	"intelligent_customer/backend/internal/db"
	dbseed "intelligent_customer/backend/internal/db/seed"
	"intelligent_customer/backend/internal/handler"
	"intelligent_customer/backend/internal/industry"
	"intelligent_customer/backend/internal/jev"
	"intelligent_customer/backend/internal/llm"
	"intelligent_customer/backend/internal/log"
	"intelligent_customer/backend/internal/middleware"
	"intelligent_customer/backend/internal/ratelimit"
	"intelligent_customer/backend/internal/rbac"
	"intelligent_customer/backend/internal/repo"
	"intelligent_customer/backend/internal/security"
	"intelligent_customer/backend/internal/seed"
	"intelligent_customer/backend/internal/server"
	"intelligent_customer/backend/internal/service"
	"intelligent_customer/backend/internal/skill"
	"intelligent_customer/backend/internal/tenant"
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

	// v2.2 PR1：预置 25 permissions + 3 roles (admin/agent/user) + 5 rate_limit_configs。
	// 幂等，二次启动重复插入会被 OR IGNORE 跳过。
	rbacSeedRes, err := dbseed.RunRBACSeed(context.Background(), conn)
	if err != nil {
		return fmt.Errorf("rbac seed: %w", err)
	}
	if rbacSeedRes.PermissionsInserted > 0 || rbacSeedRes.RolesInserted > 0 || rbacSeedRes.RateLimitsInserted > 0 {
		logger.Info().
			Int("permissions", rbacSeedRes.PermissionsInserted).
			Int("roles", rbacSeedRes.RolesInserted).
			Int("rate_limits", rbacSeedRes.RateLimitsInserted).
			Msg("seed_v2_security_layer_inserted")
	} else {
		logger.Info().Msg("seed_v2_security_layer_idempotent")
	}

	// ---- Logger aliases with component tag --------------------------------
	httpLogger := logger.With().Str("component", "http").Logger()
	svcLogger := logger.With().Str("component", "service").Logger()
	jevLogger := logger.With().Str("component", "jev").Logger()
	llmLogger := logger.With().Str("component", "llm").Logger()
	authLogger := logger.With().Str("component", "auth").Logger()
	skillLogger := logger.With().Str("component", "skill").Logger()

	// ---- Upstream clients -------------------------------------------------
	// v2.1.1: wrap every reachable provider in an llm.Router so the
	// agent loop benefits from circuit breakers + region-aware reorder
	// even when only one channel is configured. The Router satisfies
	// both ChatCompleter and ToolChatCompleter so the call sites below
	// stay unchanged.
	var llmClient llm.ChatCompleter
	var toolLLM llm.ToolChatCompleter
	llmRouter, routerChannels := buildLLMRouter(cfg, llmLogger)
	if routerChannels > 0 {
		llmClient = llmRouter
		toolLLM = llmRouter
	} else {
		// No router — keep the previous single-channel behaviour so we
		// never silently break a deployment that only configures one
		// provider. We still register the Router's ChannelStates fn
		// with the admin endpoint so the health page renders the
		// circuit state of whatever is in front of the agent.
		switch cfg.LLMProvider {
		case config.LLMProviderMiniMax:
			ac := llm.NewAnthropic(llm.AnthropicChannel{
				BaseURL: cfg.MiniMax.BaseURL,
				Model:   cfg.MiniMax.Model,
				APIKey:  cfg.MiniMax.APIKey,
				Label:   string(cfg.LLMProvider),
			}, llmLogger)
			llmClient = ac
			toolLLM = ac
		default:
			channel := cfg.ActiveLLM()
			oc := llm.NewOpenAI(llm.OpenAIChannel{
				BaseURL: channel.BaseURL,
				Model:   channel.Model,
				APIKey:  channel.APIKey,
				Label:   string(cfg.LLMProvider),
			}, llmLogger)
			llmClient = oc
			toolLLM = oc
		}
	}
	jevClient := jev.New(*cfg, jevLogger)

	logger.Info().
		Str("provider", llmClient.Identity().Provider).
		Str("model", llmClient.Identity().Name).
		Bool("jev", jevClient.Enabled()).
		Int("llm_router_channels", routerChannels).
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
	nonces := repo.NewNonces(conn)
	admins := repo.NewAdminUsers(conn)

	// Tenant — v2.1 seed tnt_default + load region/llm_* columns for
	// later use by the LLM router + Jev orchestrator.
	tenantRepo := tenant.NewRepo(conn)
	if _, err := tenantRepo.EnsureDefault(context.Background()); err != nil {
		return fmt.Errorf("ensure default tenant: %w", err)
	}
	logger.Info().Msg("tenant_default_ready")

	// ---- Skill registry (built-in + dynamic) -----------------------------
	registry := skill.NewRegistry()
	mock := &skill.MockData{DB: conn, TicketTTL: 5 * time.Minute}
	if err := skill.RegisterBuiltins(registry, mock); err != nil {
		return fmt.Errorf("register builtins: %w", err)
	}
	// Load dynamic skills from DB + filesystem.
	skillsRepo := skill.NewSkills(conn)
	invocationsRepo := skill.NewInvocations(conn)
	ticketsRepo := skill.NewTickets(conn)
	ctx := context.Background()
	if n, err := registry.LoadFromDB(ctx, conn); err != nil {
		logger.Warn().Err(err).Msg("skill_load_db_failed")
	} else if n > 0 {
		logger.Info().Int("count", n).Msg("skill_load_db_ok")
	}
	if n, err := registry.LoadFromFS("./data/skills"); err != nil {
		logger.Warn().Err(err).Msg("skill_load_fs_failed")
	} else if n > 0 {
		logger.Info().Int("count", n).Msg("skill_load_fs_ok")
	}
	logger.Info().Strs("skills", registry.Names()).Msg("skill_registry_ready")

	// Seed mock data so the read-only skills always have something to return.
	if err := skill.SeedMockData(ctx, conn, cfg.SkillDemoUserID); err != nil {
		logger.Warn().Err(err).Msg("skill_seed_mock_failed")
	}

	// ---- Services ---------------------------------------------------------
	agentCfg := agent.Config{
		MaxSteps:     cfg.AgentMaxSteps,
		MaxTokens:    cfg.AgentMaxTokens,
		MaxWallclock: cfg.AgentMaxWallclock,
		SkillTimeout: cfg.AgentSkillTimeout,
		SystemPrompt: cfg.AgentSystemPrompt,
	}
	if agentCfg.MaxSteps == 0 {
		agentCfg = agent.DefaultConfig()
	}

	agentChatSvc := &service.AgentChat{
		Convs:       convs,
		Msgs:        msgs,
		Feedbacks:   feedbacks,
		Registry:    registry,
		Invocations: invocationsRepo,
		Tickets:     ticketsRepo,
		LLM:         toolLLM,
		HandoverCfg: service.HandoverConfig{
			ConfidenceThreshold: cfg.HandoverConfidenceThreshold,
			SignalCountLimit:    cfg.MsgHandoverCountThreshold,
		},
		Cfg:    agentCfg,
		Logger: svcLogger,
	}

	adminSvc := &service.Admin{
		Convs:       convs,
		Msgs:        msgs,
		Feedback:    feedbacks,
		Invocations: invocationsRepo,
		Logger:      svcLogger,
	}
	feedbackSvc := &service.Feedback{
		Convs:    convs,
		Feedback: feedbacks,
		Msgs:     msgs,
		JEV:      jevClient,
		Logger:   svcLogger,
	}

	// ---- Auth -------------------------------------------------------------
	// v2.2 PR1：注入 adminLookup，解析老 admin token 时正确判定权限。
	// 老 admin token（仅 sub+role，无 permissions）通过 adminLookup(username) 决定
	// 是否给 ["*"] 权限，保证兼容路径不会把 admin 误降为 user。
	issuer := auth.NewIssuerWithAdminLookup(cfg.JWTSecret, cfg.JWTTTL,
		func(username string) bool {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			u, err := admins.GetByUsername(ctx, username)
			return err == nil && u != nil
		})
	authH := &handler.AuthHandlers{
		Issuer: issuer,
		Admins: admins,
		Logger: authLogger,
	}

	// ---- HTTP handlers ----------------------------------------------------
	chatH := &handler.Chat{Service: agentChatSvc, Logger: httpLogger}
	feedbackH := &handler.Feedback{Service: feedbackSvc, Logger: httpLogger}
	adminH := &handler.Admin{
		Service: adminSvc,
		Logger:  httpLogger,
		Auth:    authH,
		Issuer:  issuer,
	}
	skillAdminH := &handler.SkillAdmin{
		Repo:        skillsRepo,
		Registry:    registry,
		Invocations: invocationsRepo,
		Tickets:     ticketsRepo,
		FSDir:       "./data/skills",
		Logger:      skillLogger,
	}
	refundH := &handler.RefundConfirm{
		Tickets: ticketsRepo,
		Logger:  skillLogger,
	}

	// ---- v2.1 Jev decision layer -----------------------------------------
	jevRegistry := jev.NewRegistry()
	if n, errs := jevRegistry.LoadFromFS("./templates/jev"); len(errs) > 0 {
		for _, e := range errs {
			logger.Warn().Err(e).Msg("jev_load_fs_warning")
		}
		logger.Info().Int("count", n).Msg("jev_load_fs_ok")
	}
	if n, err := jevRegistry.LoadFromDB(context.Background(), conn); err != nil {
		logger.Warn().Err(err).Msg("jev_load_db_failed")
	} else if n > 0 {
		logger.Info().Int("count", n).Msg("jev_load_db_ok")
	}
	logger.Info().Strs("templates", templateNames(jevRegistry)).Msg("jev_registry_ready")

	localRules := jev.NewLocalRules("./data/jev_rules", jevLogger)
	if n, errs := localRules.LoadFromFS("./data/jev_rules"); len(errs) > 0 {
		for _, e := range errs {
			logger.Warn().Err(e).Msg("jev_rules_load_warning")
		}
		logger.Info().Int("count", n).Msg("jev_rules_load_ok")
	}

	decisionsRepo := jev.NewDecisionRepo(conn)
	templatesRepo := jev.NewTemplateRepo(conn)
	loopback := jev.NewDBLoopback(decisionsRepo)
	orchestrator := jev.NewOrchestrator(jev.Options{
		Registry: jevRegistry,
		Client:   jevClient,
		Fallback: jev.NewLocalRuleFallback(localRules),
		Loopback: loopback,
		Repo:     decisionsRepo,
		Logger:   jevLogger,
	})
	logger.Info().Msg("jev_orchestrator_ready")

	// v2.1.1: wire orchestrator triggers into the chat pipeline. See
	// service.AgentChat.JEV docstring for the three trigger points
	// (message.entry / pre_ingest / pre_reply) + block-on-violate.
	agentChatSvc.JEV = orchestrator
	blockOnSensitive := true
	agentChatSvc.BlockOnSensitive = &blockOnSensitive

	// Industry templates: activate general_v1 for tnt_default so the
	// orchestrator has tenant-specific jev_templates to use.
	industryLoader := industry.NewLoader("./data/industry_templates", conn)
	if _, errs := industryLoader.LoadFromFS("./data/industry_templates"); len(errs) > 0 {
		for _, e := range errs {
			logger.Warn().Err(e).Msg("industry_load_warning")
		}
	}
	if n, err := industryLoader.Activate(context.Background(), tenant.DefaultID, "general_v1", templatesRepo); err != nil {
		logger.Warn().Err(err).Msg("industry_activate_failed")
	} else if n > 0 {
		logger.Info().Int("rows", n).Msg("industry_activated")
		// Reload registry so the published templates are picked up.
		_, _ = jevRegistry.LoadFromDB(context.Background(), conn)
	}

	jevAdminH := &handler.JevAdmin{
		Registry:       jevRegistry,
		TemplateRepo:   templatesRepo,
		DecisionRepo:   decisionsRepo,
		Loopback:       loopback,
		FSTemplatesDir: "./templates/jev",
		DB:             conn,
		Logger:         jevLogger,
	}

	// ---- v2.2 PR2: AuditLogger + RBAC + RateLimit --------------------
	// PR2 默认启用：EnableRBAC / EnableRateLimit 通过 cfg 字段或硬编码控制。
	// 安全策略：基础设施层在生产全开；测试场景下也可关（main_test.go 里赋 false）。
	auditRepo := audit.NewRepo(conn)
	auditLogger, err := audit.NewLogger(auditRepo, logger)
	if err != nil {
		return fmt.Errorf("audit logger: %w", err)
	}
	logger.Info().Msg("audit_logger_ready")

	rbacRepo := rbac.NewRepo(conn)
	rateLimitRepo := ratelimit.NewRepo(conn)
	rateLimiter, err := ratelimit.NewLimiterMap(context.Background(), rateLimitRepo, ratelimit.Config{})
	if err != nil {
		return fmt.Errorf("ratelimit map: %w", err)
	}
	logger.Info().Msg("ratelimit_map_ready")

	rbacAdminH := &handler.AdminRBAC{
		Repo:      rbacRepo,
		Logger:    httpLogger,
		AuditSink: auditLogger,
	}
	auditAdminH := &handler.AdminAudit{
		Repo: auditRepo,
	}
	rateLimitAdminH := &handler.AdminRateLimit{
		Repo:       rateLimitRepo,
		Logger:     httpLogger,
		ReloadSink: rateLimiter.RequestReload,
		AuditSink:  auditLogger,
	}

	// 把 auditLogger 注入到已有 handler（adminH / skillAdminH / jevAdminH / authH）。
	adminH.AuditSink = auditLogger
	authH.AuditSink = auditLogger
	skillAdminH.AuditSink = auditLogger
	jevAdminH.AuditSink = auditLogger

	// v2.1.1 LLM health: expose circuit-breaker state under
	// /api/admin/llm/health so operators can spot a flapping channel
	// without tailing logs. When the router is wired, ChannelStates is
	// the snapshot source; otherwise we wrap the single-channel client
	// in a static "closed" snapshot so the page still renders.
	var llmHealthH *handler.LLMHealth
	llmHealthH = &handler.LLMHealth{
		States: buildLLMHealthStates(llmRouter, cfg, llmLogger),
		Logger: jevLogger,
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
		TenantRepo:      tenantRepo, // v2.1.1: TenantGuard middleware mounted under /api
		ChatHandler:     chatH,
		FeedbackHandler: feedbackH,
		AdminHandler:    adminH,
		AuthHandlers:    authH,
		SkillAdmin:      skillAdminH,
		RefundConfirm:   refundH,
		JevAdmin:        jevAdminH,
		LLMHealth:       llmHealthH,
		AppEnv:          cfg.AppEnv, // v2.2 PR1：传给 Sanitize / SecurityHeaders
		// v2.2 PR2 增量字段：
		AdminRBAC:       rbacAdminH,
		AdminAudit:      auditAdminH,
		AdminRateLimit:  rateLimitAdminH,
		RateLimiter:     rateLimiter,
		AuditLogger:     auditLogger,
		EnableRBAC:      true,  // PR2 默认开启 RBAC gate
		EnableRateLimit: true,  // PR2 默认开启 RateLimit 5 类端点
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
	ctxRun, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Background: ticket sweeper expires stale skill_pending_tickets
	// rows so /api/skills/{confirm,cancel} and the front-end agree on
	// their state. TICKET_SWEEP_INTERVAL=0 disables it.
	sweeper := &skill.TicketSweeper{
		Tickets:  ticketsRepo,
		Interval: cfg.TicketSweepInterval,
		Logger:   logger,
	}
	if cfg.TicketSweepInterval > 0 {
		go func() {
			if err := sweeper.Run(ctxRun); err != nil {
				logger.Error().Err(err).Msg("ticket_sweeper_returned_error")
			}
		}()
	} else {
		logger.Warn().Msg("ticket_sweeper_disabled_set_TICKET_SWEEP_INTERVAL")
	}

	if err := server.ListenAndServe(ctxRun, server.ListenOptions{
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

// templateNames returns the names of every template visible to the
// default tenant. Used in the boot log line so operators can see at a
// glance which templates are loaded.
func templateNames(r *jev.Registry) []string {
	tpls := r.ListByTenant("tnt_default")
	out := make([]string, 0, len(tpls))
	for _, t := range tpls {
		out = append(out, t.Name)
	}
	return out
}

// buildLLMRouter constructs an llm.Router with one channel per
// configured provider. Returns (nil, 0) when no channel can be built
// so the caller can fall back to the legacy single-channel path.
//
// Slot policy (matches docs/v2-llm-routing.md §7):
//   - The user's LLM_PROVIDER is the primary channel.
//   - Any *other* provider with a non-empty API key becomes the
//     secondary / tertiary fallback.
//   - The minimax channel is marked Regions=["cn"] so cn tenants
//     prefer it; intl tenants get it last.
//   - All channels share one CircuitBreaker with the docs default
//     (3 fails → open, 30s cooldown).
func buildLLMRouter(cfg *config.Config, logger zerolog.Logger) (*llm.Router, int) {
	var configs []llm.ChannelConfig

	// Helper to build a channel from a configured LLMChannel.
	mkChannel := func(slot, provider string, regions []string, ch config.LLMChannel) llm.ChannelConfig {
		var chImpl llm.Channel
		if provider == string(config.LLMProviderMiniMax) {
			chImpl = llm.NewAnthropic(llm.AnthropicChannel{
				BaseURL: ch.BaseURL,
				Model:   ch.Model,
				APIKey:  ch.APIKey,
				Label:   provider,
			}, logger)
		} else {
			chImpl = llm.NewOpenAI(llm.OpenAIChannel{
				BaseURL: ch.BaseURL,
				Model:   ch.Model,
				APIKey:  ch.APIKey,
				Label:   provider,
			}, logger)
		}
		return llm.ChannelConfig{
			Slot:     slot,
			Provider: provider,
			Regions:  regions,
			Channel:  chImpl,
			Breaker:  llm.NewCircuitBreaker(llm.CircuitBreakerConfig{FailureThreshold: 3, Cooldown: 30 * time.Second}),
		}
	}

	primaryProvider := string(cfg.LLMProvider)
	primaryCh := cfg.ActiveLLM()
	configs = append(configs, mkChannel("primary", primaryProvider, nil, primaryCh))

	// Secondary = the other provider, if its key is set.
	otherProvider := string(config.LLMProviderOpenAI)
	otherCh := cfg.OpenAI
	if primaryProvider == string(config.LLMProviderOpenAI) {
		otherProvider = string(config.LLMProviderMiniMax)
		otherCh = cfg.MiniMax
	}
	if otherCh.APIKey != "" {
		regions := []string{"cn"}
		if otherProvider != string(config.LLMProviderMiniMax) {
			regions = nil
		}
		configs = append(configs, mkChannel("secondary", otherProvider, regions, otherCh))
	}

	if len(configs) == 0 {
		return nil, 0
	}
	return llm.NewRouter(configs, logger), len(configs)
}

// buildLLMHealthStates returns a snapshot function suitable for
// handler.LLMHealth.States. When a Router is wired it just forwards
// ChannelStates(); otherwise we synthesise a single "closed" row so
// the admin page still renders.
func buildLLMHealthStates(router *llm.Router, cfg *config.Config, logger zerolog.Logger) func() []handler.LLMChannelHealth {
	if router != nil {
		return func() []handler.LLMChannelHealth {
			src := router.ChannelStates()
			out := make([]handler.LLMChannelHealth, 0, len(src))
			for _, c := range src {
				out = append(out, handler.LLMChannelHealth{
					Slot:     c.Slot,
					Provider: c.Provider,
					Model:    c.Model,
					State:    string(c.State),
				})
			}
			return out
		}
	}
	// Single-channel fallback: synthesise one "closed" snapshot row.
	provider := string(cfg.LLMProvider)
	ch := cfg.ActiveLLM()
	modelName := ch.Model
	return func() []handler.LLMChannelHealth {
		return []handler.LLMChannelHealth{{
			Slot:     "primary",
			Provider: provider,
			Model:    modelName,
			State:    "closed",
		}}
	}
}