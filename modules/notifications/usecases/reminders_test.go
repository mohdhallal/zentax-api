package usecases_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/modules/notifications/domain"
	"github.com/mohamadhallal/zentax-api/modules/notifications/usecases"
	"github.com/mohamadhallal/zentax-api/platform/outbox"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// These state the job's control flow — which tenants it touches, what it does
// when one of them fails, and that a second pass adds nothing. The DATABASE
// guarantees (the tenant's civil day resolved by Postgres, and the unique index
// that makes the dedupe real) are proved against live Postgres in
// acceptance/modules/notifications.

const (
	tenantBerlin   = "11111111-1111-1111-1111-111111111111"
	tenantHonolulu = "22222222-2222-2222-2222-222222222222"
)

// --- fakes ------------------------------------------------------------------

// fakeTx is the Tx seam without a database: it runs the body on the context it
// was given, which is what lets a test assert WHICH tenant the body ran for.
type fakeTx struct{ tenants []string }

func (f *fakeTx) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	f.tenants = append(f.tenants, app.GetTenantID(ctx))
	return fn(ctx)
}

type fakeTenants struct {
	days []domain.TenantDay
	at   time.Time
	err  error
}

func (f *fakeTenants) ListTenantDays(_ context.Context, at time.Time) ([]domain.TenantDay, error) {
	f.at = at
	return f.days, f.err
}

type fakeTasks struct {
	byTenant map[string][]domain.DueTask
	failFor  string
	asked    []string
}

func (f *fakeTasks) ListDueTasks(ctx context.Context, _ dateonly.Date, _ int) ([]domain.DueTask, error) {
	tenant := app.GetTenantID(ctx)
	f.asked = append(f.asked, tenant)
	if tenant == f.failFor {
		return nil, errors.New("boom")
	}
	return f.byTenant[tenant], nil
}

// fakeOutbox stands in for the unique index: a key already seen is not written
// again, and the caller is told so.
type fakeOutbox struct {
	seen      map[string]string
	envelopes []outbox.NewMessage
}

func newFakeOutbox() *fakeOutbox { return &fakeOutbox{seen: map[string]string{}} }

func (f *fakeOutbox) Enqueue(ctx context.Context, m outbox.NewMessage) (outbox.Receipt, error) {
	key := app.GetTenantID(ctx) + "|" + m.DedupeKey
	if m.DedupeKey != "" {
		if id, ok := f.seen[key]; ok {
			return outbox.Receipt{ID: id, Deduplicated: true}, nil
		}
	}
	id := "msg-" + strconv.Itoa(len(f.envelopes)+1)
	f.seen[key] = id
	f.envelopes = append(f.envelopes, m)
	return outbox.Receipt{ID: id}, nil
}

// --- fixtures ---------------------------------------------------------------

// at is the instant every case below runs at: 10:00 UTC, which is 12:00 in
// Berlin (CEST) and midnight in Honolulu (UTC-10, no DST). One instant, two
// tenants on opposite sides of the send boundary.
var at = time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)

func berlinDay() domain.TenantDay {
	return domain.TenantDay{
		TenantID: tenantBerlin, Zone: "Europe/Berlin",
		LocalDate: dateonly.New(2026, 9, 13), LocalHour: 12,
		LocalMidnight: time.Date(2026, 9, 12, 22, 0, 0, 0, time.UTC),
	}
}

func honoluluDay() domain.TenantDay {
	return domain.TenantDay{
		TenantID: tenantHonolulu, Zone: "Pacific/Honolulu",
		LocalDate: dateonly.New(2026, 9, 13), LocalHour: 0,
		LocalMidnight: time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC),
	}
}

func overdueTask(assignee string) domain.DueTask {
	return domain.DueTask{
		TaskID: "task-" + assignee, WorkflowID: "wf-1", Name: "File VAT return",
		PeriodCode: "M8", DueDate: dateonly.New(2026, 9, 1), EntityName: "Acme GmbH",
		WorkflowName: "Monthly VAT",
		AssigneeID:   assignee, AssigneeName: "Preparer", AssigneeEmail: assignee + "@acme.test",
	}
}

func job(tx *fakeTx, tenants *fakeTenants, tasks *fakeTasks, outbox *fakeOutbox) *usecases.ReminderJob {
	return usecases.NewReminderJob(tx, tenants, tasks, outbox, domain.ReminderSettings{}).
		WithClock(func() time.Time { return at })
}

// --- tests ------------------------------------------------------------------

// TestTenantBeforeItsSendHourIsNotScannedEarly is the boundary rule. Both
// tenants are in the same instant; only the one whose civil day has reached the
// send hour is read at all — the other is not "scanned and found empty", it is
// not scanned.
func TestTenantBeforeItsSendHourIsNotScannedEarly(t *testing.T) {
	t.Parallel()

	tx := &fakeTx{}
	tenants := &fakeTenants{days: []domain.TenantDay{berlinDay(), honoluluDay()}}
	tasks := &fakeTasks{byTenant: map[string][]domain.DueTask{
		tenantBerlin:   {overdueTask("u1")},
		tenantHonolulu: {overdueTask("u2")},
	}}
	outbox := newFakeOutbox()

	report, err := job(tx, tenants, tasks, outbox).Run(context.Background())
	require.NoError(t, err)

	assert.Equal(t, at, tenants.at, "the job's clock is what decides the day")
	assert.Equal(t, 2, report.TenantsConsidered)
	assert.Equal(t, 1, report.TenantsScanned)
	assert.Equal(t, 1, report.TenantsEarly)
	assert.Equal(t, []string{tenantBerlin}, tasks.asked, "the early tenant's tasks are never read")
	assert.Equal(t, []string{tenantBerlin}, tx.tenants, "and no transaction is opened for it")

	require.Len(t, outbox.envelopes, 1)
	assert.Equal(t, "u1@acme.test", outbox.envelopes[0].Recipient)
	assert.Equal(t, domain.TemplateDeadlineReminder, outbox.envelopes[0].Template)
	assert.Equal(t, "reminder:2026-09-13:u1", outbox.envelopes[0].DedupeKey)
}

// TestDueAtIsWhenTheDigestWasOwed: the outbox row is dated by the tenant's send
// hour, not by the moment the pass happened to run, so a late pass records how
// late it was.
func TestDueAtIsWhenTheDigestWasOwed(t *testing.T) {
	t.Parallel()

	tx := &fakeTx{}
	tenants := &fakeTenants{days: []domain.TenantDay{berlinDay()}}
	tasks := &fakeTasks{byTenant: map[string][]domain.DueTask{tenantBerlin: {overdueTask("u1")}}}
	outbox := newFakeOutbox()

	_, err := job(tx, tenants, tasks, outbox).Run(context.Background())
	require.NoError(t, err)

	require.Len(t, outbox.envelopes, 1)
	// Berlin's day began at 22:00 UTC the day before; 08:00 local is 06:00 UTC.
	assert.Equal(t, time.Date(2026, 9, 13, 6, 0, 0, 0, time.UTC), outbox.envelopes[0].DueAt)
}

// TestSecondPassOfTheSameDayQueuesNothing: the digest is owed once, so a second
// pass reports the population as suppressed rather than mailing it again.
func TestSecondPassOfTheSameDayQueuesNothing(t *testing.T) {
	t.Parallel()

	tenants := &fakeTenants{days: []domain.TenantDay{berlinDay()}}
	tasks := &fakeTasks{byTenant: map[string][]domain.DueTask{tenantBerlin: {overdueTask("u1")}}}
	outbox := newFakeOutbox()

	first, err := job(&fakeTx{}, tenants, tasks, outbox).Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, first.Queued)
	assert.Equal(t, 0, first.Suppressed)

	second, err := job(&fakeTx{}, tenants, tasks, outbox).Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, second.Queued)
	assert.Equal(t, 1, second.Suppressed)
	assert.Len(t, outbox.envelopes, 1, "one digest, whatever the pass count")
}

// TestOneTenantsFailureDoesNotSilenceTheRest: a compliance product cannot let
// one tenant's bad row cost every other tenant its deadlines.
func TestOneTenantsFailureDoesNotSilenceTheRest(t *testing.T) {
	t.Parallel()

	working := honoluluDay()
	working.LocalHour = 12 // past the boundary too, so both are scanned

	tenants := &fakeTenants{days: []domain.TenantDay{berlinDay(), working}}
	tasks := &fakeTasks{
		byTenant: map[string][]domain.DueTask{tenantHonolulu: {overdueTask("u2")}},
		failFor:  tenantBerlin,
	}
	outbox := newFakeOutbox()

	report, err := job(&fakeTx{}, tenants, tasks, outbox).Run(context.Background())
	require.Error(t, err, "the failure is reported")
	assert.Contains(t, err.Error(), tenantBerlin, "and names the tenant it belongs to")

	assert.Equal(t, 1, report.TenantsFailed)
	assert.Equal(t, 1, report.TenantsScanned)
	assert.Equal(t, 1, report.Queued)
	require.Len(t, outbox.envelopes, 1)
	assert.Equal(t, "u2@acme.test", outbox.envelopes[0].Recipient)
}

// TestListingTenantsFailsThePass: without the tenant list there is no pass to
// salvage, so the error is returned whole.
func TestListingTenantsFailsThePass(t *testing.T) {
	t.Parallel()

	tenants := &fakeTenants{err: errors.New("registry unreachable")}
	outbox := newFakeOutbox()

	report, err := job(&fakeTx{}, tenants, &fakeTasks{}, outbox).Run(context.Background())
	require.Error(t, err)
	assert.Equal(t, 0, report.TenantsConsidered)
	assert.Empty(t, outbox.envelopes)
}

// TestDigestLogLineIsCountsOnly runs a real pass and reads back what it wrote to
// the log.
//
// The per-digest line exists so an operator can answer "was a message written
// for this person, and what was in it?" — and the only description of a digest
// it may carry is COUNTS: the recipient, the task names and the entity names
// are personal or customer data, and a log line sits outside the erasure
// boundary (ADR-0015; ADR-0027 decision 9). The pure half of the claim is in
// domain/digest_test.go, where a helper nobody called would satisfy it forever;
// this is the half that fails when a CALL SITE starts naming the person.
//
// Deliberately not parallel: the job logs through the process logger, so the
// test swaps it and puts it back.
func TestDigestLogLineIsCountsOnly(t *testing.T) {
	var buf bytes.Buffer
	previous := logger.Log
	logger.Log = logger.New(&logger.Config{Format: "json", Level: "debug", Writer: &buf})
	t.Cleanup(func() {
		// Putting a nil back would be a trap for every other test in this
		// package: logIfConfigured installs a default through a sync.Once, and
		// this test consumed the Once without needing it. Leave a real logger.
		if previous == nil {
			logger.InitBasic()
			return
		}
		logger.Log = previous
	})

	tenants := &fakeTenants{days: []domain.TenantDay{berlinDay()}}
	tasks := &fakeTasks{byTenant: map[string][]domain.DueTask{tenantBerlin: {overdueTask("u1")}}}
	outbox := newFakeOutbox()

	_, err := job(&fakeTx{}, tenants, tasks, outbox).Run(context.Background())
	require.NoError(t, err)

	logged := buf.String()
	require.Contains(t, logged, "notifications: digest queued", "every digest written is described")
	assert.Contains(t, logged, `"digest":"overdue=1 today=0 soon=0"`, "and described as counts")
	assert.Contains(t, logged, `"messageId":"msg-1"`, "the id is the join into the outbox row")
	assert.Contains(t, logged, `"tenantId":"`+tenantBerlin+`"`)

	// The fixture's digest is FOR a named person at a named address, ABOUT named
	// work at a named entity. None of that may be anywhere in the pass's output.
	for _, personal := range []string{"u1@acme.test", "Preparer", "File VAT return", "Acme GmbH"} {
		assert.NotContains(t, logged, personal, "%q is personal or customer data and must not reach a log", personal)
	}
}
