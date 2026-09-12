package bootstrap

import (
	"context"
	"time"

	gochi "github.com/go-chi/chi/v5"
	"github.com/jmoiron/sqlx"

	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/metric"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/middlewares"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/middlewares/ratelimit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/swagger"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/auditlog"
	authpg "github.com/mohamadhallal/zentax-api/modules/auth/repositories/pg"
	authuc "github.com/mohamadhallal/zentax-api/modules/auth/usecases"
	"github.com/mohamadhallal/zentax-api/modules/datatemplates"
	"github.com/mohamadhallal/zentax-api/modules/documents"
	"github.com/mohamadhallal/zentax-api/modules/entities"
	"github.com/mohamadhallal/zentax-api/modules/entityobligations"
	"github.com/mohamadhallal/zentax-api/modules/health"
	"github.com/mohamadhallal/zentax-api/modules/identity"
	identityhandlers "github.com/mohamadhallal/zentax-api/modules/identity/handlers"
	identitypg "github.com/mohamadhallal/zentax-api/modules/identity/repositories/pg"
	identityusecases "github.com/mohamadhallal/zentax-api/modules/identity/usecases"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes"
	"github.com/mohamadhallal/zentax-api/modules/reports"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances"
	"github.com/mohamadhallal/zentax-api/modules/workflows"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/database"
	"github.com/mohamadhallal/zentax-api/platform/metrics"
	metricsmock "github.com/mohamadhallal/zentax-api/platform/metrics/mock"
	metricsprom "github.com/mohamadhallal/zentax-api/platform/metrics/prometheus"
)

type App struct {
	Router    *gochi.Mux
	Container *Container
	Config    *config.Config
	Mode      types.ServerMode
	Metrics   metrics.Recorder
	db        *sqlx.DB
}

// Close releases resources owned by the application.
func (a *App) Close() error {
	if a == nil || a.db == nil {
		return nil
	}
	db := a.db
	a.db = nil
	return db.Close()
}

func New(cfg *config.Config, mode types.ServerMode) (*App, error) {
	dbConn, err := database.ConnectDB(&cfg.Database)
	if err != nil {
		return nil, err
	}
	db := database.NewExec(dbConn)

	var metricsRecorder metrics.Recorder
	if cfg.Metrics.Enabled {
		rec, err := metricsprom.New(cfg.Metrics.Namespace)
		if err != nil {
			_ = dbConn.Close()
			return nil, err
		}
		metricsRecorder = rec
	} else {
		metricsRecorder = metricsmock.New()
	}

	// Document blob store (ADR-0022) — fs or s3 per validated config.
	store, err := NewStorage(context.Background(), cfg.Storage)
	if err != nil {
		_ = dbConn.Close()
		return nil, err
	}

	ctr := NewContainer(db, WithStorage(store, cfg.Storage.MaxUploadBytes))

	authValidator := authuc.NewInternalAuth(authpg.NewInternalAPIKeyRepo(db))

	// First-party identity/auth (ADR-0011) + machine identity (service
	// accounts / API tokens) + member administration (invites, roles). The
	// encryption key is validated at startup for deployed envs; a missing dev
	// key just disables MFA (nil key).
	encKey, _ := cfg.Auth.DecodeEncryptionKey()
	grantRepo := identitypg.NewGrantRepo(db)
	identityUC := identityusecases.NewUseCases(
		identitypg.NewUserRepo(db),
		identitypg.NewSessionRepo(db),
		identitypg.NewTokenRepo(db),
		grantRepo,
		identityusecases.Settings{
			EncryptionKey:      encKey,
			SessionIdleTTL:     time.Duration(cfg.Auth.SessionIdleTTLMinutes) * time.Minute,
			SessionAbsoluteTTL: time.Duration(cfg.Auth.SessionAbsoluteTTLHours) * time.Hour,
		},
	).
		WithMembers(identitypg.NewMemberRepo(db), identitypg.NewInviteRepo(db)).
		WithTx(db). // accept-invite opens its own tenant-bound tx (public route)
		WithAudit(audit.NewRecorder(db))
	// Account settings (ADR-0003): the tenant registry row, pinned to the
	// session's tenant; the timezone drives the reports' "today".
	tenantUC := identityusecases.NewTenantUseCases(identitypg.NewTenantRepo(db), audit.NewRecorder(db))
	cookieCfg := identityhandlers.CookieConfig{
		Name:        cfg.Auth.SessionCookieName,
		Secure:      cfg.Auth.SessionCookieSecure,
		AbsoluteTTL: time.Duration(cfg.Auth.SessionAbsoluteTTLHours) * time.Hour,
	}

	// Rate limiting (delivery/httpkit/middlewares/ratelimit): the limiter is
	// per-application state — the acceptance suite stands up several apps in one
	// process — so the global chain PROVIDES it on the request context and the
	// route builder wraps the per-route middlewares that read it back. Nothing
	// here is a package-level variable; when the limiter is absent (rate
	// limiting off, or a router a unit test built directly) every one of those
	// middlewares is a pass-through.
	limiter, err := newRateLimiter(cfg.RateLimit)
	if err != nil {
		_ = dbConn.Close()
		return nil, err
	}

	chiRouter := gochi.NewRouter()

	chiRouter.Use(middlewares.CORSMiddleware(cfg.CORS))
	chiRouter.Use(middlewares.RequestIdMiddleware)
	chiRouter.Use(middlewares.RecoveryMiddleware)
	chiRouter.Use(middlewares.MetricsMiddleware(metricsRecorder.HTTP()))
	chiRouter.Use(middlewares.RequestLoggerMiddleware)
	// After the logger and the metrics recorder, so a shed request is still
	// counted and logged as the 429 it is — the limiter must be observable, and
	// a burst it absorbs must not look like a gap in the traffic.
	if limiter != nil {
		chiRouter.Use(ratelimit.Provide(limiter))
	}

	router := routing.NewRouter(chiRouter, mode, authValidator, identityUC, cfg.Auth.SessionCookieName, grantRepo, db)

	health.RegisterRoutes(router, mode, database.NewPinger(dbConn))
	identity.RegisterRoutes(router, identityUC, identityUC, identityUC, tenantUC, cookieCfg)
	entities.RegisterRoutes(router, ctr.EntityUseCases)
	obligationtypes.RegisterRoutes(router, ctr.ObligationTypeUseCases)
	entityobligations.RegisterRoutes(router, ctr.EntityObligationUseCases)
	workflows.RegisterRoutes(router, ctr.WorkflowUseCases, ctr.WorkflowStarter)
	workflowtasks.RegisterRoutes(router, ctr.WorkflowTaskUseCases)
	taskinstances.RegisterRoutes(router, ctr.TaskInstanceUseCases)
	datatemplates.RegisterRoutes(router, ctr.DataTemplateUseCases)
	documents.RegisterRoutes(router, ctr.DocumentUseCases, cfg.Storage.MaxUploadBytes)
	reports.RegisterRoutes(router, ctr.ReportsReader)
	auditlog.RegisterRoutes(router, ctr.AuditLogReader)

	chiRouter.NotFound(httperr.NotFoundHandler())

	if cfg.Metrics.Enabled {
		metric.Mount(chiRouter, metricsRecorder.Handler())
	}

	// The OpenAPI document + UI is an explicit opt-in (development + staging;
	// production ships without it — swagger.enabled / SWAGGER_ENABLED).
	if cfg.Swagger.Enabled {
		swagger.Mount(chiRouter, router.Routes(), mode, swagger.Config{
			Title:             cfg.Swagger.Title,
			Version:           cfg.Swagger.Version,
			SessionCookieName: cfg.Auth.SessionCookieName,
		})
	}

	if cfg.IsDevelopment() {
		routing.PrintRoutes(chiRouter, mode)
	}

	return &App{
		Router:    chiRouter,
		Container: ctr,
		Config:    cfg,
		Mode:      mode,
		Metrics:   metricsRecorder,
		db:        dbConn,
	}, nil
}

// newRateLimiter builds the application's rate limiter from configuration, or
// returns nil when rate limiting is switched off (config.RateLimitConfig.Active
// — which a Config assembled in code, rather than through config.Load, leaves
// off until it calls ApplyDefaults).
//
// The store is the in-memory one: correct for a single process, and behind an
// interface precisely so a shared (Redis) implementation can replace it when a
// cell runs several API tasks — see the MemoryStore doc comment for what that
// implementation owes.
func newRateLimiter(cfg config.RateLimitConfig) (*ratelimit.Limiter, error) {
	if !cfg.Active() {
		return nil, nil
	}

	resolver, err := ratelimit.NewAddressResolver(cfg.TrustedProxies, cfg.ForwardedHeader)
	if err != nil {
		return nil, err
	}

	return ratelimit.New(
		ratelimit.NewMemoryStore(),
		ratelimit.NewGate(
			cfg.Verification.MaxConcurrent,
			cfg.Verification.MaxQueued,
			cfg.Verification.MaxWait(),
		),
		resolver,
		ratelimit.Settings{
			Anonymous:     rateLimitRule(cfg.Anonymous),
			Authenticated: rateLimitRule(cfg.Authenticated),
			Verify:        cfg.Verification.Paths,
			Exempt:        cfg.ExemptPaths,
		},
	), nil
}

func rateLimitRule(rule config.RateLimitRuleConfig) ratelimit.Rule {
	return ratelimit.Rule{
		Limit:  rule.Requests,
		Burst:  rule.Burst,
		Window: rule.Window(),
	}
}
