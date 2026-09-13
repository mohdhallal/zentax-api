package bootstrap

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/mohamadhallal/zentax-api/logger"
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
	notificationsdomain "github.com/mohamadhallal/zentax-api/modules/notifications/domain"
	notificationspg "github.com/mohamadhallal/zentax-api/modules/notifications/repositories/pg"
	notificationsusecases "github.com/mohamadhallal/zentax-api/modules/notifications/usecases"
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes"
	"github.com/mohamadhallal/zentax-api/modules/reports"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances"
	"github.com/mohamadhallal/zentax-api/modules/workflows"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/database"
	"github.com/mohamadhallal/zentax-api/platform/mail"
	"github.com/mohamadhallal/zentax-api/platform/mail/logmail"
	"github.com/mohamadhallal/zentax-api/platform/mail/sesmail"
	"github.com/mohamadhallal/zentax-api/platform/mail/smtpmail"
	"github.com/mohamadhallal/zentax-api/platform/metrics"
	metricsmock "github.com/mohamadhallal/zentax-api/platform/metrics/mock"
	metricsprom "github.com/mohamadhallal/zentax-api/platform/metrics/prometheus"
	"github.com/mohamadhallal/zentax-api/platform/outbox"
	"github.com/mohamadhallal/zentax-api/platform/scheduler"
)

type App struct {
	Router    *gochi.Mux
	Container *Container
	Config    *config.Config
	Mode      types.ServerMode
	Metrics   metrics.Recorder
	// Scheduler is the in-process recurring work (the reminder scan, outbox
	// delivery, pruning). It is BUILT here but never STARTED here:
	// bootstrap.New is also what the acceptance suite calls, twice per test,
	// and a test must not enter a leadership race or run background work.
	// cmd/server runs it; nothing else does.
	Scheduler *scheduler.Scheduler
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
		WithAudit(audit.NewRecorder(db)).
		// Invite delivery: the activation link is QUEUED on the invite's own
		// transaction, so the token and the message that carries it commit
		// together; the scheduled runner sends it. Until this was wired an
		// invite ended in the API response, for an administrator to copy by
		// hand — which is still where the cleartext token is, for now.
		//
		// The queue is SEALED with the same key the identity use cases above
		// take: the queued message holds the live credential, and it holds it
		// as ciphertext until the runner opens it to send (platform/outbox
		// seal.go).
		WithInviteMail(notificationsusecases.NewNotifier(outbox.NewQueue(db).WithSeal(outbox.SealFromKey(encKey))))
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

	// Recurring work runs inside this process, with one task elected runner
	// through a Postgres advisory lock. Built here, started by cmd/server.
	sched, err := newScheduler(cfg, dbConn, db, metricsRecorder)
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
		Scheduler: sched,
		db:        dbConn,
	}, nil
}

// schedulerLease is the advisory-lock namespace the runner election contends
// on. Every API task of one deployment uses this one name against one database,
// so exactly one of them runs scheduled work.
const schedulerLease = "zentax:scheduler"

// Job cadences. Delivery is frequent because a reminder is only useful before
// its deadline; pruning is rare because it exists to keep a drained table small.
//
// The reminder scan is HOURLY, and that is not a compromise between "daily" and
// "often": its send boundary is an hour of each TENANT'S civil day, and tenants
// are spread over some 27 hours of offsets, so a pass that ran once a day at a
// fixed UTC instant would serve exactly one timezone well. Running it hourly
// costs one indexed query per active tenant per hour and is idempotent by
// construction — a person is owed one digest per local day, and the outbox's
// dedupe key, not the cadence, is what enforces that.
const (
	outboxDeliveryInterval = 15 * time.Second
	outboxPruneInterval    = 6 * time.Hour
	reminderScanInterval   = time.Hour
)

// newScheduler assembles the in-process scheduled work.
//
// PRODUCERS and DELIVERY are separate jobs, and the separation is the point of
// the queue. The reminder scan writes what the product owes whether or not the
// transport is healthy; delivery drains it. A provider outage therefore costs a
// delay, not a deadline nobody was ever told about — the messages sit pending
// and go out when the transport recovers.
//
// The scheduler is BUILT here and started by cmd/server; nothing it registers
// runs, and no leadership is contended for, until Run is called.
func newScheduler(cfg *config.Config, conn *sqlx.DB, db *database.Exec, recorder metrics.Recorder) (*scheduler.Scheduler, error) {
	// The deadline reminder: one digest per person per tenant-local day,
	// written to the outbox. See modules/notifications.
	// One repository, two ports: the tenant enumeration is control-plane (the
	// registry carries no RLS) and the task read is tenant-scoped, so the job
	// sees them as separate seams even though one type serves both.
	// One seal for both halves of the queue: what the producer writes sealed,
	// the delivery loop must be able to open. A digest payload is a tenant's
	// compliance calendar, so it is protected at rest for the same reason the
	// invite's credential is (platform/outbox seal.go).
	encKey, _ := cfg.Auth.DecodeEncryptionKey()
	payloadSeal := outbox.SealFromKey(encKey)

	scan := notificationspg.NewScanRepo(db)
	reminders := notificationsusecases.NewReminderJob(
		db, scan, scan, outbox.NewQueue(db).WithSeal(payloadSeal), notificationsdomain.ReminderSettings{},
	)
	jobs := []scheduler.Job{
		{
			Name:     reminders.Name(),
			Interval: reminderScanInterval,
			Timeout:  5 * time.Minute,
			Run: func(ctx context.Context) error {
				_, err := reminders.Run(ctx)
				return err
			},
		},
	}

	sender, err := mailSender(cfg)
	if err != nil {
		return nil, err
	}
	store := outbox.NewStore(db, db).WithAudit(audit.NewRecorder(db))
	// The operator's MAIL_*_TIMEOUT_MS reaches the transport only through here:
	// the dispatcher always sets a deadline, and an adapter applies its own
	// timeout only when the caller sets none.
	dispatcher := outbox.NewDispatcher(store, sender, recorder.Client(), outbox.Settings{
		SendTimeout: cfg.Mail.SendTimeout(),
	}).WithSeal(payloadSeal)
	jobs = append(jobs,
		scheduler.Job{Name: "outbox.delivery", Interval: outboxDeliveryInterval, Run: dispatcher.DeliverDue},
		scheduler.Job{Name: "outbox.prune", Interval: outboxPruneInterval, Timeout: time.Minute, Run: dispatcher.PruneSettled},
	)

	sched := scheduler.New(scheduler.NewAdvisoryLease(conn, schedulerLease), recorder.Client(), scheduler.Settings{})
	for _, job := range jobs {
		if err := sched.Register(job); err != nil {
			return nil, err
		}
	}
	return sched, nil
}

// mailSender builds the outbound mail transport and adapts it to the outbox's
// seam — the composition root's job, and the only place the two halves meet.
//
// A Config assembled in code rather than loaded from disk (the acceptance
// harness does exactly that) has never seen ApplyDefaults, so the defaults are
// applied to a local copy here. The result for such a config is the log driver,
// which is also what development gets; config.Load refuses a deployed tier on
// it, so "the mailer that does not send" can never be what production boots.
func mailSender(cfg *config.Config) (outbox.Sender, error) {
	mailCfg := cfg.Mail
	mailCfg.ApplyDefaults()

	var replyTo mail.Address
	if mailCfg.ReplyTo != "" {
		replyTo = mail.Address{Email: mailCfg.ReplyTo}
	}
	brand, err := mail.NewBrand(
		mailCfg.ProductName,
		cfg.App.PublicBaseURL,
		mail.Address{Name: mailCfg.FromName, Email: mailCfg.FromAddress},
		replyTo,
	)
	if err != nil {
		return nil, err
	}

	transport, err := mailTransport(mailCfg)
	if err != nil {
		return nil, err
	}
	logger.Log.Info("Outbound mail configured", logger.String("driver", mailCfg.Driver))

	return outboxMailer{brand: brand, transport: transport}, nil
}

// mailTransport picks the adapter the configuration names. Each one fails
// CLOSED at construction — a missing SMTP host, an unverifiable relay, an
// unresolvable AWS credential chain — so a mail misconfiguration is a boot
// failure rather than a notification that silently never arrives.
func mailTransport(cfg config.MailConfig) (mail.Sender, error) {
	switch cfg.Driver {
	case config.MailDriverSMTP:
		return smtpmail.New(smtpmail.Options{
			Host:          cfg.SMTP.Host,
			Port:          cfg.SMTP.Port,
			Username:      cfg.SMTP.Username,
			Password:      cfg.SMTP.Password,
			Auth:          smtpmail.AuthMethod(cfg.SMTP.Auth),
			TLS:           smtpmail.TLSMode(cfg.SMTP.TLS),
			AllowInsecure: cfg.SMTP.AllowInsecure,
			SkipTLSVerify: cfg.SMTP.SkipTLSVerify,
			CAFile:        cfg.SMTP.CAFile,
			Timeout:       time.Duration(cfg.SMTP.TimeoutMs) * time.Millisecond,
			LocalName:     cfg.SMTP.LocalName,
		})
	case config.MailDriverSES:
		return sesmail.New(context.Background(), sesmail.Options{
			Region:           cfg.SES.Region,
			ConfigurationSet: cfg.SES.ConfigurationSet,
			Endpoint:         cfg.SES.Endpoint,
			Timeout:          time.Duration(cfg.SES.TimeoutMs) * time.Millisecond,
		})
	case config.MailDriverLog:
		return logmail.New(logger.Log), nil
	default:
		return nil, fmt.Errorf("bootstrap: unknown mail driver %q", cfg.Driver)
	}
}

// outboxMailer is the join between the queue and the transport: it renders the
// message's FROZEN payload with the template the message names, then hands the
// result to the adapter.
//
// Rendering here — at delivery, from the snapshot — is what makes a redelivery
// identical to the original rather than a second, differently-worded mail, and
// it is why a duplicate is harmless.
//
// It also translates verdicts. The two seams classify failure in their own
// vocabularies; this is the one place that maps one onto the other, so neither
// package has to know the other's error types and the delivery loop never
// string-matches a provider's prose.
type outboxMailer struct {
	brand     mail.Brand
	transport mail.Sender
}

var _ outbox.Sender = outboxMailer{}

func (m outboxMailer) Send(ctx context.Context, msg outbox.Message) error {
	rendered, err := mail.RenderNamed(m.brand, mail.Address{Email: msg.Recipient}, msg.Template, msg.Payload)
	if err != nil {
		// A cell may run before its custom domain exists (ADR-0025), and every
		// template carries an absolute link, so "no public origin" is a
		// condition of the DEPLOYMENT that clears the moment an operator sets
		// one — the message waits. Dead-lettering it would throw away an
		// invitation over a setting, and the attempt budget still ends it if
		// nobody ever fixes the deployment.
		if errors.Is(err, mail.ErrNoBaseURL) {
			// Anything not marked permanent is retried by the dispatcher, so
			// returning the bare error is what makes the message wait.
			return fmt.Errorf("render %s: %w", msg.Template, err)
		}
		// A template this build does not know or a payload the renderer cannot
		// read is a property of the row: it does not get better by waiting.
		return outbox.Permanent("render "+msg.Template, err)
	}
	if err := m.transport.Send(ctx, rendered); err != nil {
		if mail.IsPermanent(err) {
			return outbox.Permanent("transport rejected the message", err)
		}
		return err
	}
	return nil
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
