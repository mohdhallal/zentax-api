package bootstrap

import (
	"context"
	"time"

	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/platform/database"
	"github.com/mohamadhallal/zentax-api/platform/scheduler"
	"github.com/mohamadhallal/zentax-api/platform/securityevent"
)

// securityEventMaintenanceTimeout bounds one pass. The ordinary pass is a single
// catalogue query; the expensive shape is a first run after a long gap, which
// may create a year of missing months and drop several. Every step of it is
// idempotent and resumable — what was created stays created, what was dropped is
// gone — so cutting the pass off costs a repeat, never a half-finished state,
// and a job that overran must not hold up the reminder scan behind it.
const securityEventMaintenanceTimeout = 2 * time.Minute

// newSecurityEventRetentionJob assembles the thirteen-month retention of the
// authentication stream (ADR-0007), or returns nil when the deployment has
// switched it off.
//
// It is registered here rather than left to an operator, a cron entry or the
// migrate job for the reason the WORM export is: three separate records — the
// ADR, the table comment and the operations runbook — already stated this
// retention as a fact, and a documented control with no code behind it is worse
// than an undocumented gap, because the record is what stops anyone looking.
// The log line below is deliberately unconditional: an operator reading a boot
// log can tell whether the window is running and what it is, without reading an
// ADR to find out what it should be.
func newSecurityEventRetentionJob(cfg *config.Config, db *database.Exec) *scheduler.Job {
	if !cfg.SecurityEvents.RetentionActive() {
		logger.Log.Warn("Security-event retention is disabled; the authentication stream will grow without bound and client_ip will be bounded by nothing",
			logger.String("remedy", "unset "+config.EnvSecurityEventsRetentionEnabled+" (or set it true) unless a jurisdiction genuinely requires the stream kept indefinitely"))
		return nil
	}

	retention := securityevent.NewRetention(db, securityevent.RetentionSettings{
		RetentionMonths: cfg.SecurityEvents.RetentionMonths,
		MonthsAhead:     cfg.SecurityEvents.PartitionMonthsAhead,
	})

	logger.Log.Info("Security-event retention configured",
		logger.Int("retentionMonths", cfg.SecurityEvents.RetentionMonths),
		logger.Int("partitionMonthsAhead", cfg.SecurityEvents.PartitionMonthsAhead),
		logger.String("mechanism", "monthly partition drop"),
		logger.String("interval", securityEventMaintenanceInterval.String()),
	)

	return &scheduler.Job{
		Name:     retention.Name(),
		Interval: securityEventMaintenanceInterval,
		Timeout:  securityEventMaintenanceTimeout,
		Run: func(ctx context.Context) error {
			_, err := retention.Run(ctx)
			return err
		},
	}
}
