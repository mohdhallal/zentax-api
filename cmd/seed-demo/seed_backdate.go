package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mohamadhallal/zentax-api/app"
	platformseed "github.com/mohamadhallal/zentax-api/platform/seed"
	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
)

// The completion escape hatch — the seeder's ONE direct-database pass over
// data the API owns.
//
// Why it has to exist: no endpoint accepts a completion instant. completed_at,
// submitted_at and approved_at are Postgres NOW() in the task-instance
// repository, so a demo whose VAT return for January was filed in February can
// only be built by finishing the work now and then moving the instants back.
// The alternative — engineering "late" and "on time" purely out of deadlines
// relative to today — would make every completion date in the demo today's,
// which is exactly the thing a screenshot of a compliance heatmap must not
// show.
//
// What it must NOT touch is audit_log. The ledger is append-only and
// hash-chained (ADR-0007 / ADR-0008): the audit trail legitimately records the
// instants at which the seed run really happened, and rewriting it would break
// the chain. The seeder says so in its output rather than quietly leaving a
// contradiction between the two.
//
// Every statement runs inside a transaction bound to the tenant
// (SELECT set_config('app.tenant_id', …, true) — platform/database.Exec does
// this for a context carrying the tenant), because the application role is
// NOBYPASSRLS. The parent table task_instances is addressed, never one of its
// 16 hash partitions.

// backdateSQL rewrites one instance's completion instants.
//
// COALESCE keeps a column the dataset says nothing about: a via=put completion
// has no submitted_at, and passing NULL positionally would clear the value the
// API legitimately set.
//
// The predicate names tenant_id as well as id even though row-level security
// already confines the statement to the bound tenant: task_instances is
// partitioned by HASH(tenant_id) (ADR-0020), so the explicit equality lets
// Postgres prune to the one partition instead of touching all sixteen. The
// PARENT table is addressed — never a partition by name, which would be an
// implementation detail of that partitioning.
const backdateSQL = `
UPDATE task_instances
   SET completed_at = COALESCE($3, completed_at),
       submitted_at = COALESCE($4, submitted_at),
       approved_at  = COALESCE($5, approved_at),
       updated_at   = $6
 WHERE tenant_id = $1 AND id = $2`

// backdateRow is one instance's target instants.
type backdateRow struct {
	// InstanceID is the task instance to rewrite.
	InstanceID string
	// Ref locates it in the dataset ("acme.w1 M1/prepare") for error messages.
	Ref string
	// CompletedAt / SubmittedAt / ApprovedAt are UTC instants; nil leaves the
	// column as the API wrote it.
	CompletedAt *time.Time
	SubmittedAt *time.Time
	ApprovedAt  *time.Time
}

// updatedAt is the instant the row's last legitimate change happened: the
// completion, or failing that the submission.
func (r backdateRow) updatedAt() time.Time {
	if r.CompletedAt != nil {
		return *r.CompletedAt
	}
	if r.SubmittedAt != nil {
		return *r.SubmittedAt
	}
	return time.Time{}
}

// instantsFor computes the instants the dataset declares for one instance.
//
// The rule is the dataset's own ($schemaNotes.conventions.dates): a completion
// is a CIVIL date in the tenant's timezone, read at the local completion time
// (the instance's completedAtLocalTime, else the dataset default, else 10:00),
// then converted to UTC. submitted_at is the submission's civil date at the
// dataset default time; approved_at equals completed_at.
//
// ok is false when the instance declares no instants at all (an in_progress or
// pending_approval instance keeps the API's own stamps).
func instantsFor(inst spec.Instance, zone *time.Location, datasetDefault string) (backdateRow, bool, error) {
	if zone == nil {
		return backdateRow{}, false, fmt.Errorf("no timezone for the tenant")
	}
	var row backdateRow

	if !inst.CompletedOn.IsZero() {
		completedAt, ok := inst.CompletionInstant(zone, datasetDefault)
		if !ok {
			return backdateRow{}, false, fmt.Errorf(
				"completedOn %s at %q is not a civil date and time this zone can read",
				inst.CompletedOn, inst.LocalCompletionTime(datasetDefault))
		}
		// The dataset may state the exact instant it expects (it does wherever
		// the tenant-zone civil date differs from the UTC one). Disagreeing with
		// it means the seeder and the verifier would build different worlds, so
		// stop rather than write the wrong instant.
		if !inst.CompletedAtUtc.IsZero() && !completedAt.Equal(inst.CompletedAtUtc.UTC()) {
			return backdateRow{}, false, fmt.Errorf(
				"completedAtUtc says %s but %s at %s in %s is %s",
				inst.CompletedAtUtc.UTC().Format(time.RFC3339), inst.CompletedOn,
				inst.LocalCompletionTime(datasetDefault), zone, completedAt.Format(time.RFC3339))
		}
		row.CompletedAt = &completedAt
		if inst.Via == spec.ViaApprove {
			approvedAt := completedAt
			row.ApprovedAt = &approvedAt
		}
	}

	if !inst.SubmittedOn.IsZero() {
		submitted := spec.Instance{CompletedOn: inst.SubmittedOn}
		submittedAt, ok := submitted.CompletionInstant(zone, datasetDefault)
		if !ok {
			return backdateRow{}, false, fmt.Errorf(
				"submittedOn %s at %q is not a civil date and time this zone can read",
				inst.SubmittedOn, datasetDefault)
		}
		row.SubmittedAt = &submittedAt
	}

	if row.CompletedAt == nil && row.SubmittedAt == nil {
		return backdateRow{}, false, nil
	}
	return row, true, nil
}

// backdateCompletions applies one tenant's rows in a single tenant-bound
// transaction: either every instant moves or none does. It returns the number
// of rows rewritten.
func backdateCompletions(
	ctx context.Context, db platformseed.DB, tenantID string, rows []backdateRow,
) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	if db == nil {
		return 0, fmt.Errorf("no database connection")
	}

	updated := 0
	err := db.WithinTransaction(app.WithTenantID(ctx, tenantID), func(txCtx context.Context) error {
		for _, row := range rows {
			result, err := db.ExecContext(txCtx, backdateSQL,
				tenantID, row.InstanceID, row.CompletedAt, row.SubmittedAt, row.ApprovedAt, row.updatedAt())
			if err != nil {
				return fmt.Errorf("%s (instance %s): %w", row.Ref, row.InstanceID, err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("%s (instance %s): read rows affected: %w", row.Ref, row.InstanceID, err)
			}
			if affected != 1 {
				return fmt.Errorf("%s (instance %s): updated %d rows, want exactly 1 "+
					"(the id is wrong, or row-level security hid it from this connection)",
					row.Ref, row.InstanceID, affected)
			}
			updated++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("back-date completion instants: %w", err)
	}
	return updated, nil
}
