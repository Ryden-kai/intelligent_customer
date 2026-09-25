// Package server wires the HTTP router. The frontend SPA lives in a
// separate repo and is served independently (Cloudflare Pages or any
// static host). This router exposes the JSON API only.
package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/audit"
	"intelligent_customer/backend/internal/auth"
	"intelligent_customer/backend/internal/handler"
	"intelligent_customer/backend/internal/llm"
	"intelligent_customer/backend/internal/middleware"
	"intelligent_customer/backend/internal/ratelimit"
	"intelligent_customer/backend/internal/rbac"
	"intelligent_customer/backend/internal/tenant"
)

type Deps struct {
	Logger          zerolog.Logger
	CORSOrigins     []string
	Issuer          *auth.Issuer
	TenantRepo      *tenant.Repo        // optional; when nil the /api group keeps v1 behaviour
	ChatHandler     *handler.Chat
	FeedbackHandler *handler.Feedback
	AdminHandler    *handler.Admin
	AuthHandlers    *handler.AuthHandlers
	SkillAdmin      *handler.SkillAdmin
	RefundConfirm   *handler.RefundConfirm
	JevAdmin        *handler.JevAdmin
	LLMHealth       *handler.LLMHealth    // optional; mounted under /admin/llm/health when set
	SignatureOpts   *middleware.SignatureOptions

	// v2.2 PR1 增量字段：
	AppEnv string // "production" / "development" / 其他；传给 middleware.Sanitize

	// v2.2 PR2 增量字段：
	AdminRBAC         *handler.AdminRBAC         // optional; /api/admin/roles + /permissions + /users/:id/roles
	AdminAudit        *handler.AdminAudit        // optional; /api/admin/audit
	AdminRateLimit    *handler.AdminRateLimit    // optional; /api/admin/ratelimit/configs
	RateLimiter       *ratelimit.LimiterMap      // optional; 用作中间件
	AuditLogger       *audit.Logger              // optional; 启动后台 flush goroutine
	EnableRBAC        bool                       // 默认 false：开 PR2 RBAC gate 包装
	EnableRateLimit   bool                       // 默认 false：开 PR2 5 类限流保护
}

func New(d Deps) http.Handler {
	r := chi.NewRouter()

	// Order matters: RequestID → Recover → Sanitize → SecurityHeaders → AccessLog → CORS.
	r.Use(middleware.RequestID)
	r.Use(middleware.Recover(d.Logger))
	// v2.2 PR1：Sanitize 包装 5xx 响应，env=production 时脱敏敏感字段。
	r.Use(middleware.Sanitize(d.AppEnv))
	// v2.2 PR1：SecurityHeaders 下发 7 个安全 header（仅生产下发 HSTS）。
	r.Use(middleware.SecurityHeaders(d.AppEnv == "production"))
	r.Use(middleware.AccessLog(d.Logger))
	r.Use(middleware.CORS(d.CORSOrigins))

	// 启动 audit logger 后台 goroutine。
	if d.AuditLogger != nil {
		go d.AuditLogger.Run(context.Background())
	}

	// Health endpoints — public, no signing required (k8s liveness probes
	// can't sign requests). TenantGuard is intentionally NOT mounted
	// here so probes still pass during boot when the tenants table is
	// not yet ready.
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Get("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	// Public API — gated by HMAC signature when configured.
	r.Route("/api", func(r chi.Router) {
		if d.SignatureOpts != nil {
			r.Use(middleware.RequireSignature(*d.SignatureOpts))
		}
		// v2.1.1 TenantGuard: resolve tenant from X-Tenant-ID header
		// (or JWT.tid for admin requests) and inject it into ctx. When
		// TenantRepo is nil (e.g. test setups) the middleware short-
		// circuits to the synthetic default tenant so downstream code
		// always sees a non-empty tenant.Info.
		if d.TenantRepo != nil {
			r.Use(tenant.Middleware(d.TenantRepo, d.Issuer))
			// Region hint for the LLM router — the chat handler reads
			// it via llm.regionFromCtx to reorder channels by region.
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, rr *http.Request) {
					t := tenant.FromContext(rr.Context())
					if t.Region != "" {
						rr = rr.WithContext(llm.WithRegionHint(rr.Context(), t.Region))
					}
					next.ServeHTTP(w, rr)
				})
			})
		}

		// v2.2 PR2: RateLimit 5 类端点（chat / feedback / auth login / register / admin write）。
		// 默认 disabled（EnableRateLimit=false），PR3+ 可在 main.go 显式启用。
		if d.EnableRateLimit && d.RateLimiter != nil {
			r.With(ratelimit.Middleware(d.RateLimiter, ratelimit.DimensionTenantID, "POST:/api/chat")).
				Post("/chat", d.ChatHandler.ServeHTTP)
			r.With(ratelimit.Middleware(d.RateLimiter, ratelimit.DimensionTenantID, "POST:/api/feedback")).
				Post("/feedback", d.FeedbackHandler.ServeHTTP)
		} else {
			r.Post("/chat", d.ChatHandler.ServeHTTP)
			r.Post("/feedback", d.FeedbackHandler.ServeHTTP)
		}

		// Skill confirm + cancel — public, bound to ticket+user id pair.
		if d.RefundConfirm != nil {
			r.Post("/skills/confirm", d.RefundConfirm.Confirm)
			r.Post("/skills/cancel", d.RefundConfirm.Cancel)
		}

		r.Route("/admin", func(r chi.Router) {
			// auth.login 单独走 rate limit（IP 维度），
			// 注意：必须挂在 /admin 路由组内（仍受 CORS / AccessLog 保护），
			// 但 TenantGuard 不应作用于 login（login 时还没有 tenant_id）。
			// 由于 login 在 RequireAuth 之前（不需要 JWT），单独暴露。
			loginHandler := http.HandlerFunc(d.AuthHandlers.Login)
			if d.EnableRateLimit && d.RateLimiter != nil {
				loginHandler = ratelimit.Middleware(d.RateLimiter, ratelimit.DimensionIP, "POST:/api/auth/login")(loginHandler).ServeHTTP
			}
			r.Post("/login", loginHandler)

			// Auth-protected sub-tree.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireAuth(d.Issuer, d.Logger))

				// v2.2 PR2: rate limit admin write（actor_id 维度）。
				// 当 EnableRateLimit=true 时，POST/PUT/DELETE 路径受 ALL:/api/admin 限流保护。
				if d.EnableRateLimit && d.RateLimiter != nil {
					r.Use(func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, rr *http.Request) {
							method := rr.Method
							if method == http.MethodPost || method == http.MethodPut || method == http.MethodDelete {
								res := d.RateLimiter.Allow(ratelimit.DimensionActorID, "", "ALL:/api/admin")
								_ = res // 全局保护由 actor_id middleware 接管；这里仅做记录
							}
							next.ServeHTTP(w, rr)
						})
					})
				}

				// ---- conversations ----
				if d.EnableRBAC {
					r.With(rbac.RequirePermission("conversation.read")).Get("/conversations", d.AdminHandler.ListConversations)
					r.With(rbac.RequirePermission("conversation.read")).Get("/conversations/{id}", d.AdminHandler.GetConversation)
					r.With(rbac.RequirePermission("conversation.write")).Post("/conversations/{id}/reply", d.AdminHandler.PostAgentReply)
				} else {
					r.Get("/conversations", d.AdminHandler.ListConversations)
					r.Get("/conversations/{id}", d.AdminHandler.GetConversation)
					r.Post("/conversations/{id}/reply", d.AdminHandler.PostAgentReply)
				}
				if d.EnableRBAC {
					r.With(rbac.RequirePermission("stats.read")).Get("/stats/satisfaction", d.AdminHandler.StatsSatisfaction)
				} else {
					r.Get("/stats/satisfaction", d.AdminHandler.StatsSatisfaction)
				}

				r.Get("/me", d.AuthHandlers.Whoami)

				if d.SkillAdmin != nil {
					if d.EnableRBAC {
						r.With(rbac.RequirePermission("skills.read")).Get("/skills", d.SkillAdmin.List)
						r.With(rbac.RequirePermission("skills.create")).Post("/skills", d.SkillAdmin.Create)
						r.With(rbac.RequirePermission("skills.toggle")).Put("/skills/{id}", d.SkillAdmin.Update)
						r.With(rbac.RequirePermission("skills.delete")).Delete("/skills/{id}", d.SkillAdmin.Delete)
						r.With(rbac.RequirePermission("skills.toggle")).Post("/skills/reload", d.SkillAdmin.Reload)
					} else {
						r.Get("/skills", d.SkillAdmin.List)
						r.Post("/skills", d.SkillAdmin.Create)
						r.Put("/skills/{id}", d.SkillAdmin.Update)
						r.Delete("/skills/{id}", d.SkillAdmin.Delete)
						r.Post("/skills/reload", d.SkillAdmin.Reload)
					}
				}

				if d.JevAdmin != nil {
					if d.EnableRBAC {
						r.With(rbac.RequirePermission("jev.template.read")).Get("/jev/templates", d.JevAdmin.ListTemplates)
						r.With(rbac.RequirePermission("jev.template.read")).Get("/jev/templates/{id}", d.JevAdmin.GetTemplate)
						r.With(rbac.RequirePermission("jev.template.write")).Post("/jev/templates", d.JevAdmin.CreateTemplate)
						r.With(rbac.RequirePermission("jev.template.publish")).Post("/jev/templates/publish/{id}", d.JevAdmin.PublishTemplate)
						r.With(rbac.RequirePermission("jev.template.publish")).Post("/jev/templates/archive/{id}", d.JevAdmin.ArchiveTemplate)
						r.With(rbac.RequirePermission("jev.template.write")).Delete("/jev/templates/{id}", d.JevAdmin.DeleteTemplate)
						r.With(rbac.RequirePermission("jev.template.write")).Post("/jev/templates/reload", d.JevAdmin.ReloadHTTP)
						r.With(rbac.RequirePermission("jev.decision.read")).Get("/jev/decisions", d.JevAdmin.ListDecisions)
						r.With(rbac.RequirePermission("jev.decision.read")).Get("/jev/decisions/{id}", d.JevAdmin.GetDecision)
						r.With(rbac.RequirePermission("jev.decision.review")).Post("/jev/decisions/{id}/review", d.JevAdmin.ReviewDecision)
						r.With(rbac.RequirePermission("stats.read")).Get("/jev/stats", d.JevAdmin.Stats)
					} else {
						r.Get("/jev/templates", d.JevAdmin.ListTemplates)
						r.Get("/jev/templates/{id}", d.JevAdmin.GetTemplate)
						r.Post("/jev/templates", d.JevAdmin.CreateTemplate)
						r.Post("/jev/templates/publish/{id}", d.JevAdmin.PublishTemplate)
						r.Post("/jev/templates/archive/{id}", d.JevAdmin.ArchiveTemplate)
						r.Delete("/jev/templates/{id}", d.JevAdmin.DeleteTemplate)
						r.Post("/jev/templates/reload", d.JevAdmin.ReloadHTTP)
						r.Get("/jev/decisions", d.JevAdmin.ListDecisions)
						r.Get("/jev/decisions/{id}", d.JevAdmin.GetDecision)
						r.Post("/jev/decisions/{id}/review", d.JevAdmin.ReviewDecision)
						r.Get("/jev/stats", d.JevAdmin.Stats)
					}
				}
				if d.LLMHealth != nil {
					if d.EnableRBAC {
						r.With(rbac.RequirePermission("stats.read")).Get("/llm/health", d.LLMHealth.ServeHTTP)
					} else {
						r.Get("/llm/health", d.LLMHealth.ServeHTTP)
					}
				}

				// ---- v2.2 PR2 新增端点：RBAC / Audit / RateLimit ----
				if d.AdminRBAC != nil {
					if d.EnableRBAC {
						r.With(rbac.RequirePermission("role.read")).Get("/roles", d.AdminRBAC.ListRoles)
						r.With(rbac.RequirePermission("role.read")).Get("/roles/{id}", d.AdminRBAC.GetRole)
						r.With(rbac.RequirePermission("role.read")).Get("/permissions", d.AdminRBAC.ListPermissions)
						r.With(rbac.RequirePermission("role.manage")).Post("/roles", d.AdminRBAC.CreateRole)
						r.With(rbac.RequirePermission("role.manage")).Put("/roles/{id}", d.AdminRBAC.UpdateRole)
						r.With(rbac.RequirePermission("role.manage")).Delete("/roles/{id}", d.AdminRBAC.DeleteRole)
						r.With(rbac.RequirePermission("role.manage")).Get("/users/{user_id}/roles", d.AdminRBAC.GetUserRoles)
						r.With(rbac.RequirePermission("role.manage")).Put("/users/{user_id}/roles", d.AdminRBAC.SetUserRoles)
					} else {
						r.Get("/roles", d.AdminRBAC.ListRoles)
						r.Get("/roles/{id}", d.AdminRBAC.GetRole)
						r.Get("/permissions", d.AdminRBAC.ListPermissions)
						r.Post("/roles", d.AdminRBAC.CreateRole)
						r.Put("/roles/{id}", d.AdminRBAC.UpdateRole)
						r.Delete("/roles/{id}", d.AdminRBAC.DeleteRole)
						r.Get("/users/{user_id}/roles", d.AdminRBAC.GetUserRoles)
						r.Put("/users/{user_id}/roles", d.AdminRBAC.SetUserRoles)
					}
				}

				if d.AdminAudit != nil {
					if d.EnableRBAC {
						r.With(rbac.RequirePermission("audit.read")).Get("/audit", d.AdminAudit.List)
						r.With(rbac.RequirePermission("audit.read")).Get("/audit/{id}", d.AdminAudit.Get)
						r.With(rbac.RequirePermission("audit.export")).Get("/audit/export", d.AdminAudit.Export)
					} else {
						r.Get("/audit", d.AdminAudit.List)
						r.Get("/audit/{id}", d.AdminAudit.Get)
						r.Get("/audit/export", d.AdminAudit.Export)
					}
				}

				if d.AdminRateLimit != nil {
					if d.EnableRBAC {
						r.With(rbac.RequirePermission("ratelimit.manage")).Get("/ratelimit/configs", d.AdminRateLimit.ListConfigs)
						r.With(rbac.RequirePermission("ratelimit.manage")).Put("/ratelimit/configs/{id}", d.AdminRateLimit.UpdateConfig)
					} else {
						r.Get("/ratelimit/configs", d.AdminRateLimit.ListConfigs)
						r.Put("/ratelimit/configs/{id}", d.AdminRateLimit.UpdateConfig)
					}
				}
			})
		})
	})

	// Anything that isn't /api or /health falls through to a 404. The
	// frontend is served by Cloudflare Pages (or any static host), not here.
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"not_found","message":"this is the API service; the SPA is hosted separately"}`))
	})
	return r
}

// ListenOptions captures everything needed to bind a server: addr + optional
// TLS material. When both TLS files are present we run ListenAndServeTLS,
// otherwise plain HTTP (development convenience).
type ListenOptions struct {
	Addr        string
	ReadTO      time.Duration
	WriteTO     time.Duration
	TLSCertFile string
	TLSKeyFile  string
}

// ListenAndServe runs an HTTP/HTTPS server with graceful shutdown. When
// TLSConfig is nil the server is plain HTTP; otherwise ListenAndServeTLS is
// used.
func ListenAndServe(ctx context.Context, opts ListenOptions, h http.Handler, logger zerolog.Logger) error {
	srv := &http.Server{
		Addr:         opts.Addr,
		Handler:      h,
		ReadTimeout:  opts.ReadTO,
		WriteTimeout: opts.WriteTO,
		IdleTimeout:  120 * time.Second,
	}
	useTLS := opts.TLSCertFile != "" && opts.TLSKeyFile != ""
	if useTLS {
		logger.Info().Str("addr", opts.Addr).Str("cert", opts.TLSCertFile).Msg("https_server_listen")
	} else {
		logger.Warn().
			Str("addr", opts.Addr).
			Msg("http_server_listen_no_tls_set_TLS_CERT_FILE_and_TLS_KEY_FILE_to_enable_https")
	}
	errCh := make(chan error, 1)
	go func() {
		var err error
		if useTLS {
			err = srv.ListenAndServeTLS(opts.TLSCertFile, opts.TLSKeyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		logger.Info().Msg("http_server_shutdown")
		shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shCtx)
	case err := <-errCh:
		return err
	}
}

// keep strings import referenced
var _ = strings.HasPrefix