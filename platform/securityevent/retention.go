package securityevent

import (
	"context"
	"fmt"
	"sync"

	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// JobName identifies the retention job in the scheduler, in its metrics
// operation label, and in the acceptance test that asserts the job is actually
// registered — because a job nobody registered is a control nobody runs, and
// that is precisely the state this file was written to end.
const JobName = "securityevents.retention"

// Retention is the scheduled partition maintenance of the authentication
// stream: the thirteen-month window of ADR-0007, which until now existed only
// as prose in an ADR, a table comment and an operations runbook.
//
// WHAT IT ACTUALLY DOES, and why it is not a DELETE. security_events is
// append-only twice over — no DELETE policy under FORCE RLS, plus a BEFORE
// DELETE row trigger (SQLSTATE ZT031) that refuses every role including one
// that bypasses RLS. A row-wise prune is therefore not the slower option, it is
// the impossible one: it would have to begin by dismantling the append-only
// guarantee the stream's integrity rests on. Dropping a whole month fires no
// trigger, consults no policy, costs an unlink rather than a dead tuple per row,
// and leaves nothing for a VACUUM to chase. It is the only deletion this table
// admits, which is why the table was partitioned by month in the first place.
//
// The drop itself happens inside a SECURITY DEFINER function
// (security_events_maintain_partitions, migration 20260913000035): the API
// connects as a role that is NOSUPERUSER, NOBYPASSRLS and — the part that
// matters here — not the OWNER of security_events, and DROP TABLE requires
// ownership, which no grant can confer. The function takes a window in months
// rather than an instant and hard-codes the table, so nothing a caller supplies
// reaches an identifier or a cutoff; it refuses a window under twelve months;
// and it never touches security_events_default or a bound it cannot read. The
// care platform/outbox's prune takes about pending messages is taken here about
// a month that could still hold a row inside the window.
//
// THE SAME RUN CREATES. The drop half is worthless alone: a month with no
// partition of its own lands in security_events_default, which retention must
// never drop, and its rows are then outside the reach of the window forever
// while every record still says the stream ages out. The invariant this job
// holds is therefore the whole one — there is a partition for every month inside
// the retention window and for every month of the run-ahead — rather than the
// drop half with the create half left to a deploy script that was never told
// about this table.
type Retention struct {
	db       database.ExecerPg
	settings RetentionSettings
}

// RetentionSettings is the window, as configuration resolved it.
type RetentionSettings struct {
	// RetentionMonths is the promise. A month can only be aged out whole, so it
	// is a FLOOR and not an exact age: a partition goes only once its NEWEST
	// possible row is past the window, which puts a row's real life between
	// RetentionMonths and RetentionMonths+1. The rounding is deliberately in the
	// direction of keeping.
	RetentionMonths int
	// MonthsAhead is how far the same run creates forward.
	MonthsAhead int
}

// MinRetentionMonths mirrors the floor the schema enforces (SQLSTATE ZT032) and
// config validation refuses a boot under. It is repeated here so a Retention
// assembled in code — a test, a future CLI — cannot ask the database for a
// window the product does not offer and get an opaque SQL error for an answer.
const MinRetentionMonths = 12

func NewRetention(db database.ExecerPg, s RetentionSettings) *Retention {
	return &Retention{db: db, settings: s}
}

// Name is the scheduler job name.
func (r *Retention) Name() string { return JobName }

// RetentionResult is what one pass did, named partition by partition. Empty is
// the normal, and the quiet, outcome: the drop set only moves on the first of a
// month, so most runs have nothing to do and say nothing.
type RetentionResult struct {
	// Created — months that did not have a partition and now do.
	Created []string
	// Dropped — months wholly past the window, gone with every address in them.
	Dropped []string
	// Deferred — a lock could not be taken in time because the login path was
	// busy. Nothing was skipped permanently; the next run picks it up.
	Deferred []string
	// Blocked — a month inside the window that could NOT be given a partition,
	// almost always because security_events_default already absorbed rows for
	// it. Those rows can no longer age out, which makes this the one outcome an
	// operator has to act on.
	Blocked []string
}

// Empty reports whether the pass changed nothing.
func (r RetentionResult) Empty() bool {
	return len(r.Created) == 0 && len(r.Dropped) == 0 && len(r.Deferred) == 0 && len(r.Blocked) == 0
}

type maintenanceRow struct {
	Action    string `db:"action"`
	Partition string `db:"partition_name"`
}

const maintainPartitions = `SELECT action, partition_name FROM security_events_maintain_partitions($1, $2)`

// Run performs one maintenance pass and logs only what happened.
func (r *Retention) Run(ctx context.Context) (RetentionResult, error) {
	var out RetentionResult
	if err := r.settings.validate(); err != nil {
		return out, err
	}

	var rows []maintenanceRow
	if err := r.db.SelectContext(ctx, &rows, maintainPartitions,
		r.settings.RetentionMonths, r.settings.MonthsAhead); err != nil {
		return out, fmt.Errorf("securityevent: maintain partitions: %w", err)
	}

	for _, row := range rows {
		switch row.Action {
		case "created":
			out.Created = append(out.Created, row.Partition)
		case "dropped":
			out.Dropped = append(out.Dropped, row.Partition)
		case "deferred":
			out.Deferred = append(out.Deferred, row.Partition)
		case "blocked":
			out.Blocked = append(out.Blocked, row.Partition)
		default:
			// A vocabulary this build does not know is a schema newer than the
			// binary. Say so rather than count it as nothing.
			return out, fmt.Errorf("securityevent: maintain partitions returned an unknown action %q for %s",
				row.Action, row.Partition)
		}
	}

	r.report(ctx, out)
	return out, nil
}

// report says what the pass did, and says NOTHING when it did nothing. A job
// that logs a line every six hours to announce that a monthly boundary has not
// been crossed is a job whose real messages nobody reads.
func (r *Retention) report(ctx context.Context, res RetentionResult) {
	log := logIfConfigured(ctx)

	// The drop is the one line an auditor will look for: it is the moment the
	// retention promise is kept, and the moment a set of network addresses stops
	// existing. Partition names are months, not personal data.
	if len(res.Dropped) > 0 {
		log.Info("Security-event partitions past the retention window were dropped",
			logger.Strings("partitions", res.Dropped),
			logger.Int("retentionMonths", r.settings.RetentionMonths))
	}
	if len(res.Created) > 0 {
		log.Info("Security-event partitions created",
			logger.Strings("partitions", res.Created),
			logger.Int("monthsAhead", r.settings.MonthsAhead))
	}
	if len(res.Deferred) > 0 {
		log.Info("Security-event partition maintenance deferred a month; the table was busy and the next run will retry",
			logger.Strings("partitions", res.Deferred))
	}
	if len(res.Blocked) > 0 {
		log.Error("Security-event months could not be given a partition — their rows are in security_events_default and can no longer age out",
			logger.Strings("partitions", res.Blocked),
			logger.String("remedy", "these months were written while partition maintenance was not running; the rows must be moved or the default partition rebuilt before retention can reach them"))
	}
}

var loggerInit sync.Once

// logIfConfigured returns the process logger, initialising a default one if
// nothing has — the same guard platform/audit/worm's exporter carries, for the
// same reason: scheduled work must not panic on its first log line, and the one
// line this job most needs to emit is the one saying a month of authentication
// evidence has just stopped existing.
func logIfConfigured(ctx context.Context) logger.Logger {
	loggerInit.Do(func() {
		if logger.Log == nil {
			logger.InitBasic()
		}
	})
	return logger.Log.WithContext(ctx)
}

func (s RetentionSettings) validate() error {
	if s.RetentionMonths < MinRetentionMonths {
		return fmt.Errorf("securityevent: a retention window of %d months is under the %d-month floor",
			s.RetentionMonths, MinRetentionMonths)
	}
	if s.MonthsAhead < 1 {
		return fmt.Errorf("securityevent: a partition run-ahead of %d months would let a month arrive with nowhere to land",
			s.MonthsAhead)
	}
	return nil
}
