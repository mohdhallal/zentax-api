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

	chiRouter := gochi.NewRouter()

	chiRouter.Use(middlewares.CORSMiddleware(cfg.CORS))
	chiRouter.Use(middlewares.RequestIdMiddleware)
	chiRouter.Use(middlewares.RecoveryMiddleware)
	chiRouter.Use(middlewares.MetricsMiddleware(metricsRecorder.HTTP()))
	chiRouter.Use(middlewares.RequestLoggerMiddleware)

	router := routing.NewRouter(chiRouter, mode, authValidator, identityUC, cfg.Auth.SessionCookieName, grantRepo, db)

	health.RegisterRoutes(router, mode)
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

	swagger.Mount(chiRouter, router.Routes(), mode, swagger.Config{
		Title:             cfg.Swagger.Title,
		Version:           cfg.Swagger.Version,
		SessionCookieName: cfg.Auth.SessionCookieName,
	})

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
