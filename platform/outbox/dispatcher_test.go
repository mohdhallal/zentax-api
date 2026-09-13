package outbox

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/logger"
)

func TestMain(m *testing.M) {
	logger.Init(&logger.Config{Level: "error", Writer: io.Discard})
	os.Exit(m.Run())
}

// fixedNow is the clock every dispatcher test runs on, so a computed retry
// instant is an exact assertion rather than an approximation.
var fixedNow = time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)

// --- doubles ---------------------------------------------------------------

type rescheduleCall struct {
	id     string
	at     time.Time
	reason Reason
}

type deadCall struct {
	id     string
	reason Reason
}

type fakeStore struct {
	mu sync.Mutex

	claim    []Message
	claimErr error
	limit    int
	lease    time.Duration

	sent        []string
	rescheduled []rescheduleCall
	dead        []deadCall
	settleErr   error

	released []string
	// releaseCtx is the error state of the context a release was handed, so a
	// test can prove the write is not itself cancelled by the shutdown that
	// caused it.
	releaseCtx  error
	releaseErr  error
	releaseSeen bool

	pruneOlderThan time.Duration
	pruneRemoved   int64
	pruneErr       error
}

func (f *fakeStore) ClaimDue(_ context.Context, limit int, lease time.Duration) ([]Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.limit, f.lease = limit, lease
	return f.claim, f.claimErr
}

func (f *fakeStore) ReleaseClaim(ctx context.Context, msgs []Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseSeen = true
	f.releaseCtx = ctx.Err()
	for i := range msgs {
		f.released = append(f.released, msgs[i].ID)
	}
	return f.releaseErr
}

func (f *fakeStore) MarkSent(_ context.Context, m Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m.ID)
	return f.settleErr
}

func (f *fakeStore) Reschedule(_ context.Context, m Message, at time.Time, r Reason) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rescheduled = append(f.rescheduled, rescheduleCall{m.ID, at, r})
	return f.settleErr
}

func (f *fakeStore) MarkDead(_ context.Context, m Message, r Reason) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dead = append(f.dead, deadCall{m.ID, r})
	return f.settleErr
}

func (f *fakeStore) Prune(_ context.Context, olderThan time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruneOlderThan = olderThan
	return f.pruneRemoved, f.pruneErr
}

type fakeSender struct {
	mu      sync.Mutex
	seen    []Message
	respond func(m Message) error
}

func (f *fakeSender) Send(_ context.Context, m Message) error {
	f.mu.Lock()
	f.seen = append(f.seen, m)
	respond := f.respond
	f.mu.Unlock()
	if respond == nil {
		return nil
	}
	return respond(m)
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

// hangingSender is the provider these budget tests are aimed at: it takes the
// message and then says nothing at all, returning only when the context it was
// given is cancelled. That is the shape of an overloaded relay, a blackholed
// route or a half-open socket — the failure that has no error to report because
// nothing ever comes back.
type hangingSender struct {
	mu   sync.Mutex
	seen []string
	// hangs decides which messages go unanswered. Nil means every one of them.
	hangs func(m Message) bool
}

func (h *hangingSender) Send(ctx context.Context, m Message) error {
	h.mu.Lock()
	h.seen = append(h.seen, m.ID)
	hangs := h.hangs
	h.mu.Unlock()

	if hangs != nil && !hangs(m) {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}

// attempted is the messages that reached the transport, in order.
func (h *hangingSender) attempted() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.seen...)
}

type spyRecorder struct {
	mu   sync.Mutex
	seen map[string]int
}

func newSpyRecorder() *spyRecorder { return &spyRecorder{seen: map[string]int{}} }

func (s *spyRecorder) ObserveDuration(client, operation string, _ time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen[client+"/"+operation]++
}

func (s *spyRecorder) count(operation string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[metricClient+"/"+operation]
}

// dispatcher builds a Dispatcher on the fixed clock with jitter switched off.
func dispatcher(store MessageStore, sender Sender, rec *spyRecorder) *Dispatcher {
	d := NewDispatcher(store, sender, rec, Settings{
		BatchSize:  10,
		ClaimLease: time.Minute,
		Backoff:    Backoff{Base: time.Minute, Max: time.Hour, MaxAttempts: 4},
	})
	d.now = func() time.Time { return fixedNow }
	return d
}

// budgeted builds a Dispatcher on the REAL clock — the budgets under test are
// elapsed time, so a frozen clock would prove nothing — with windows small
// enough to make a hung send a fast test.
func budgeted(store MessageStore, sender Sender, rec *spyRecorder, send, batch time.Duration) *Dispatcher {
	return NewDispatcher(store, sender, rec, Settings{
		BatchSize:   10,
		ClaimLease:  time.Minute,
		SendTimeout: send,
		BatchBudget: batch,
		Backoff:     Backoff{Base: time.Minute, Max: time.Hour, MaxAttempts: 4},
	})
}

func pending(id string, attempts int) Message {
	return Message{
		ID:        id,
		TenantID:  "11111111-1111-1111-1111-111111111111",
		Channel:   ChannelEmail,
		Template:  "deadline.reminder",
		Recipient: "cfo@example.test",
		Status:    StatusPending,
		Attempts:  attempts,
	}
}

// --- delivery --------------------------------------------------------------

func TestDeliverDueSendsAndSettlesEachMessage(t *testing.T) {
	t.Parallel()

	store := &fakeStore{claim: []Message{pending("a", 1), pending("b", 1)}}
	sender := &fakeSender{}
	rec := newSpyRecorder()

	require.NoError(t, dispatcher(store, sender, rec).DeliverDue(context.Background()))

	assert.Equal(t, []string{"a", "b"}, store.sent)
	assert.Equal(t, 2, sender.count())
	assert.Equal(t, 2, rec.count(opDelivered))
	assert.Equal(t, 1, rec.count(opClaim))
	assert.Equal(t, 10, store.limit, "the batch size is the claim's limit")
	assert.Equal(t, time.Minute, store.lease)
}

func TestDeliverDueWithNothingDueTouchesNothing(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	sender := &fakeSender{}

	require.NoError(t, dispatcher(store, sender, newSpyRecorder()).DeliverDue(context.Background()))

	assert.Zero(t, sender.count())
	assert.Empty(t, store.sent)
}

func TestDeliverDueRetriesATransientFailureOnAnIncreasingDelay(t *testing.T) {
	t.Parallel()

	// attempts is the count AFTER the claim charged this attempt, so a message
	// on its first attempt waits one base delay, its second two, its third four.
	for _, tc := range []struct {
		attempts int
		wait     time.Duration
	}{
		{attempts: 1, wait: time.Minute},
		{attempts: 2, wait: 2 * time.Minute},
		{attempts: 3, wait: 4 * time.Minute},
	} {
		store := &fakeStore{claim: []Message{pending("a", tc.attempts)}}
		sender := &fakeSender{respond: func(Message) error { return errors.New("smtp: 421 service unavailable") }}
		rec := newSpyRecorder()

		require.NoError(t, dispatcher(store, sender, rec).DeliverDue(context.Background()))

		require.Len(t, store.rescheduled, 1)
		assert.Equal(t, fixedNow.Add(tc.wait), store.rescheduled[0].at)
		assert.Equal(t, ReasonTransient, store.rescheduled[0].reason.Class)
		assert.Contains(t, store.rescheduled[0].reason.Detail, "421")
		assert.Empty(t, store.dead, "a transient failure must not kill the message")
		assert.Equal(t, 1, rec.count(opRetried))
	}
}

func TestDeliverDueDeadLettersAPermanentFailureOnTheFirstAttempt(t *testing.T) {
	t.Parallel()

	store := &fakeStore{claim: []Message{pending("a", 1)}}
	sender := &fakeSender{respond: func(Message) error {
		return Permanent("recipient rejected", errors.New("550 5.1.1 no such user"))
	}}
	rec := newSpyRecorder()

	require.NoError(t, dispatcher(store, sender, rec).DeliverDue(context.Background()))

	require.Len(t, store.dead, 1)
	assert.Equal(t, ReasonPermanent, store.dead[0].reason.Class)
	assert.Empty(t, store.rescheduled, "there is nothing to wait for")
	assert.Equal(t, 1, rec.count(opDead))
}

func TestDeliverDueGivesUpWhenTheAttemptBudgetIsSpent(t *testing.T) {
	t.Parallel()

	// MaxAttempts is 4 in these tests: the fourth failure is the last.
	store := &fakeStore{claim: []Message{pending("a", 3), pending("b", 4)}}
	sender := &fakeSender{respond: func(Message) error { return errors.New("smtp: timeout") }}
	rec := newSpyRecorder()

	require.NoError(t, dispatcher(store, sender, rec).DeliverDue(context.Background()))

	require.Len(t, store.rescheduled, 1)
	assert.Equal(t, "a", store.rescheduled[0].id, "one attempt left: still owed")
	require.Len(t, store.dead, 1)
	assert.Equal(t, "b", store.dead[0].id)
	assert.Equal(t, ReasonExhausted, store.dead[0].reason.Class)
	assert.Equal(t, 1, rec.count(opDead))
	assert.Equal(t, 1, rec.count(opRetried))
}

func TestDeliverDueFinishesTheBatchWhenOneMessageFails(t *testing.T) {
	t.Parallel()

	store := &fakeStore{claim: []Message{pending("a", 1), pending("b", 1), pending("c", 1)}}
	sender := &fakeSender{respond: func(m Message) error {
		if m.ID == "b" {
			return errors.New("smtp: temporary")
		}
		return nil
	}}

	require.NoError(t, dispatcher(store, sender, newSpyRecorder()).DeliverDue(context.Background()))

	assert.Equal(t, []string{"a", "c"}, store.sent, "one bad message must not strand the rest")
	require.Len(t, store.rescheduled, 1)
	assert.Equal(t, "b", store.rescheduled[0].id)
}

// A send failure is ordinary. What the JOB reports as failure is being unable to
// write down what happened — a broken queue, not a bad afternoon at a mail host.
func TestDeliverDueReportsOnlyUnsettledOutcomes(t *testing.T) {
	t.Parallel()

	store := &fakeStore{claim: []Message{pending("a", 1)}, settleErr: errors.New("deadlock detected")}
	rec := newSpyRecorder()

	err := dispatcher(store, &fakeSender{}, rec).DeliverDue(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 of 1")
	assert.Equal(t, 1, rec.count(opSettleErr))
	assert.Equal(t, 1, rec.count(opDelivered), "the send still happened and is still counted")
}

func TestDeliverDueSurfacesAClaimFailure(t *testing.T) {
	t.Parallel()

	store := &fakeStore{claimErr: errors.New("connection refused")}
	sender := &fakeSender{}

	err := dispatcher(store, sender, newSpyRecorder()).DeliverDue(context.Background())

	require.ErrorContains(t, err, "connection refused")
	assert.Zero(t, sender.count())
}

// Shutdown mid-send: the transport was interrupted, not the message rejected.
// Nothing is written down, and the claim goes back with its attempt refunded, so
// the next runner finds the message exactly as it was.
func TestSendInterruptedByShutdownIsNotCharged(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	store := &fakeStore{claim: []Message{pending("a", 1)}}
	sender := &fakeSender{respond: func(Message) error {
		cancel()
		return context.Canceled
	}}

	require.NoError(t, dispatcher(store, sender, newSpyRecorder()).DeliverDue(ctx))

	assert.Empty(t, store.sent)
	assert.Empty(t, store.rescheduled)
	assert.Empty(t, store.dead)
	assert.Equal(t, []string{"a"}, store.released, "an interrupted send leaves the claim to be handed back")
}

// --- time budgets ----------------------------------------------------------

// The defect this bounds: with no per-message budget, one unanswered send held
// the transport for the whole pass, so the other 49 messages of a batch spent
// an attempt each — charged by the claim — without ever reaching the transport,
// and were dead-lettered in about a quarter of an hour having never been sent.
func TestAHungSendCostsItsOwnWindowAndNoMore(t *testing.T) {
	t.Parallel()

	store := &fakeStore{claim: []Message{pending("a", 1)}}
	sender := &hangingSender{}
	rec := newSpyRecorder()

	start := time.Now()
	require.NoError(t, budgeted(store, sender, rec, 50*time.Millisecond, 5*time.Second).DeliverDue(context.Background()))
	elapsed := time.Since(start)

	assert.Less(t, elapsed, time.Second, "the per-message budget, not the pass's, is what bounds one send")
	assert.Equal(t, []string{"a"}, sender.attempted(), "the cancellation reached the provider call itself")
	assert.Equal(t, 1, rec.count(opTimedOut))
}

// And the other half of the same defect: a send that never answers used to have
// NO terminal state — never sent, never dead, its attempt spent, and first in
// the queue again on the next pass. Silence for a whole window is now a failed
// attempt like any other: retried on the backoff...
func TestAHungSendIsARetryableFailureWithItsOwnReason(t *testing.T) {
	t.Parallel()

	store := &fakeStore{claim: []Message{pending("a", 1)}}
	rec := newSpyRecorder()

	require.NoError(t, budgeted(store, &hangingSender{}, rec, 50*time.Millisecond, 5*time.Second).
		DeliverDue(context.Background()))

	require.Len(t, store.rescheduled, 1)
	assert.Equal(t, ReasonTimeout, store.rescheduled[0].reason.Class,
		"a provider that went silent is not the same operator problem as one that said no")
	assert.Contains(t, store.rescheduled[0].reason.Detail, "timed out")
	assert.Empty(t, store.released, "the message was tried; its claim is spent")
	assert.Equal(t, 1, rec.count(opRetried))
}

// ...and given up on when the attempt budget runs out, so a message can never
// hang in the queue for ever: it ends somewhere a human can explain.
func TestAMessageThatOnlyEverHangsIsEventuallyDeadLettered(t *testing.T) {
	t.Parallel()

	// MaxAttempts is 4 in these tests, and the claim already charged the fourth.
	store := &fakeStore{claim: []Message{pending("a", 4)}}
	rec := newSpyRecorder()

	require.NoError(t, budgeted(store, &hangingSender{}, rec, 50*time.Millisecond, 5*time.Second).
		DeliverDue(context.Background()))

	require.Len(t, store.dead, 1)
	assert.Equal(t, ReasonExhausted, store.dead[0].reason.Class)
	assert.Contains(t, store.dead[0].reason.Detail, "timed out", "the row says WHY it was given up on")
	assert.Empty(t, store.rescheduled)
	assert.Equal(t, 1, rec.count(opDead))
}

func TestAHungSendDoesNotStrandTheRestOfTheBatch(t *testing.T) {
	t.Parallel()

	store := &fakeStore{claim: []Message{pending("a", 1), pending("b", 1), pending("c", 1)}}
	sender := &hangingSender{hangs: func(m Message) bool { return m.ID == "a" }}

	require.NoError(t, budgeted(store, sender, newSpyRecorder(), 50*time.Millisecond, 5*time.Second).
		DeliverDue(context.Background()))

	assert.Equal(t, []string{"b", "c"}, store.sent, "the pass carries on past the message that went quiet")
	require.Len(t, store.rescheduled, 1)
	assert.Equal(t, "a", store.rescheduled[0].id)
	assert.Empty(t, store.released)
}

// A pass that runs out of budget hands back what it never reached, ATTEMPT AND
// ALL. An attempt the transport never saw is not an attempt, and charging one
// is how a slow provider used to exhaust the retry budget of every message
// queued behind it.
func TestMessagesThePassNeverReachedGoBackToTheQueueUnspent(t *testing.T) {
	t.Parallel()

	store := &fakeStore{claim: []Message{pending("a", 1), pending("b", 1), pending("c", 1)}}
	sender := &hangingSender{} // nothing answers, so the first send eats the budget.
	rec := newSpyRecorder()

	require.NoError(t, budgeted(store, sender, rec, 40*time.Millisecond, 60*time.Millisecond).
		DeliverDue(context.Background()))

	assert.Equal(t, []string{"a"}, sender.attempted(), "only the message that had a full window was tried")
	require.Len(t, store.rescheduled, 1, "and only that one is charged for the silence")
	assert.Equal(t, []string{"b", "c"}, store.released)
	assert.Equal(t, 1, rec.count(opReleased))
}

// Shutdown mid-batch: every claimed row — the one in flight included — is left
// in a state the next runner can pick up at once, rather than a row that looks
// attempted and stays invisible until its lease expires.
func TestShutdownMidBatchHandsEveryUnfinishedRowBack(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &fakeStore{claim: []Message{pending("a", 1), pending("b", 1), pending("c", 1)}}
	sender := &hangingSender{}

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel() // the process goes down while "a" is still on the wire.
	}()
	require.NoError(t, budgeted(store, sender, newSpyRecorder(), 5*time.Second, 10*time.Second).DeliverDue(ctx))

	assert.Equal(t, []string{"a"}, sender.attempted())
	assert.Empty(t, store.sent)
	assert.Empty(t, store.rescheduled)
	assert.Empty(t, store.dead, "shutdown is not the provider's failure and must not be charged as one")
	assert.Equal(t, []string{"a", "b", "c"}, store.released)
	assert.NoError(t, store.releaseCtx, "the hand-back must survive the shutdown that caused it")
}

// A pass too small to fit even one send must still try one — and must never
// charge it for a window the pass itself clipped. Silence, and a queue that
// never moves, is the one outcome an operator cannot diagnose.
func TestAPassTooSmallForOneSendStillTriesAndChargesNothing(t *testing.T) {
	t.Parallel()

	store := &fakeStore{claim: []Message{pending("a", 1), pending("b", 1)}}
	sender := &hangingSender{}

	require.NoError(t, budgeted(store, sender, newSpyRecorder(), time.Second, 30*time.Millisecond).
		DeliverDue(context.Background()))

	assert.Equal(t, []string{"a"}, sender.attempted())
	assert.Empty(t, store.rescheduled, "the window was ours to clip, so the silence is not the provider's fault")
	assert.Equal(t, []string{"a", "b"}, store.released)
}

// The caller's deadline is a budget too, and the tighter of the two wins: the
// scheduler bounds the job (15s in production, its interval), and a pass must
// finish inside that rather than being killed part-way through one.
func TestTheCallersDeadlineCapsThePassBudget(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	store := &fakeStore{claim: []Message{pending("a", 1), pending("b", 1)}}
	sender := &hangingSender{}

	start := time.Now()
	require.NoError(t, budgeted(store, sender, newSpyRecorder(), 40*time.Millisecond, time.Hour).DeliverDue(ctx))

	assert.Less(t, time.Since(start), time.Second, "an hour of budget cannot outlive a caller who gave 60ms")
	assert.Equal(t, []string{"a"}, sender.attempted())
	assert.Equal(t, []string{"b"}, store.released)
}

// A hand-back that cannot be written is the queue being broken, and the job says
// so — those rows stay leased until they expire, which is exactly what the
// caller needs to know.
func TestAFailedHandBackIsReportedByTheJob(t *testing.T) {
	t.Parallel()

	store := &fakeStore{claim: []Message{pending("a", 1)}, releaseErr: errors.New("deadlock detected")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := budgeted(store, &hangingSender{}, newSpyRecorder(), time.Second, time.Minute).DeliverDue(ctx)

	require.ErrorContains(t, err, "deadlock detected")
	assert.ErrorContains(t, err, "return 1 unattempted messages to the queue")
}

// The one duplicate the design can avoid: a message the transport ACCEPTED must
// be recorded as sent even if shutdown lands in the same instant, or the next
// runner sends it again.
func TestASuccessfulSendIsRecordedEvenDuringShutdown(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	store := &fakeStore{claim: []Message{pending("a", 1)}}
	sender := &fakeSender{respond: func(Message) error {
		cancel() // the process is going down as the provider says yes.
		return nil
	}}

	require.NoError(t, dispatcher(store, sender, newSpyRecorder()).DeliverDue(ctx))

	assert.Equal(t, []string{"a"}, store.sent)
}

func TestDeliverDueStopsClaimingWorkOnceCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &fakeStore{claim: []Message{pending("a", 1), pending("b", 1)}}
	sender := &fakeSender{}

	require.NoError(t, dispatcher(store, sender, newSpyRecorder()).DeliverDue(ctx))

	assert.Zero(t, sender.count(), "a cancelled pass sends nothing")
	assert.Equal(t, []string{"a", "b"}, store.released, "and hands the whole batch straight back")
}

// --- pruning ---------------------------------------------------------------

func TestPruneSettledUsesTheRetentionWindow(t *testing.T) {
	t.Parallel()

	store := &fakeStore{pruneRemoved: 7}
	d := NewDispatcher(store, &fakeSender{}, newSpyRecorder(), Settings{Retention: 48 * time.Hour})

	require.NoError(t, d.PruneSettled(context.Background()))
	assert.Equal(t, 48*time.Hour, store.pruneOlderThan)
}

func TestPruneSettledSurfacesFailures(t *testing.T) {
	t.Parallel()

	store := &fakeStore{pruneErr: errors.New("permission denied")}
	d := NewDispatcher(store, &fakeSender{}, newSpyRecorder(), Settings{})

	require.ErrorContains(t, d.PruneSettled(context.Background()), "permission denied")
}

// --- backoff ---------------------------------------------------------------

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	t.Parallel()

	b := Backoff{Base: time.Minute, Max: time.Hour, MaxAttempts: 8}
	for _, tc := range []struct {
		attempts int
		want     time.Duration
	}{
		{attempts: -1, want: time.Minute},
		{attempts: 0, want: time.Minute},
		{attempts: 1, want: time.Minute},
		{attempts: 2, want: 2 * time.Minute},
		{attempts: 3, want: 4 * time.Minute},
		{attempts: 6, want: 32 * time.Minute},
		{attempts: 7, want: time.Hour},
		{attempts: 8, want: time.Hour},
		{attempts: 1000, want: time.Hour},
	} {
		assert.Equal(t, tc.want, b.Delay(tc.attempts), "attempts=%d", tc.attempts)
	}
}

func TestDefaultBackoffJittersWithinTenPercent(t *testing.T) {
	t.Parallel()

	b := DefaultBackoff()
	for range 200 {
		d := b.Delay(1)
		assert.GreaterOrEqual(t, d, 54*time.Second)
		assert.LessOrEqual(t, d, 66*time.Second)
	}
}

func TestZeroValueSettingsFallBackToTheDefaults(t *testing.T) {
	t.Parallel()

	s := Settings{}.withDefaults()
	assert.Equal(t, DefaultBatchSize, s.BatchSize)
	assert.Equal(t, DefaultClaimLease, s.ClaimLease)
	assert.Equal(t, DefaultRetention, s.Retention)
	assert.Equal(t, DefaultMaxAttempts, s.Backoff.MaxAttempts)
	assert.Equal(t, DefaultSendTimeout, s.SendTimeout, "a send is never unbounded, even unconfigured")
	assert.Equal(t, DefaultBatchBudget, s.BatchBudget)
	assert.Less(t, s.BatchBudget, s.ClaimLease, "a pass must finish inside the lease it claimed under")
}

// Two settings that would break the loop if taken literally, and are not.
func TestUnworkableBudgetsAreBroughtBackIntoRange(t *testing.T) {
	t.Parallel()

	// A pass longer than its own lease would settle rows a second runner had
	// already re-claimed.
	overrunning := Settings{ClaimLease: 30 * time.Second, BatchBudget: time.Hour}.withDefaults()
	assert.Equal(t, 20*time.Second, overrunning.BatchBudget)

	// A send bigger than the pass containing it would deliver nothing at all.
	oversized := Settings{BatchBudget: 5 * time.Second, SendTimeout: time.Minute}.withDefaults()
	assert.Equal(t, 5*time.Second, oversized.SendTimeout)
}

// --- failure classification ------------------------------------------------

func TestPermanentIsRecognisedThroughWrapping(t *testing.T) {
	t.Parallel()

	err := Permanent("recipient rejected", errors.New("550"))
	assert.True(t, IsPermanent(err))
	assert.True(t, IsPermanent(errors.Join(errors.New("context"), err)))
	assert.False(t, IsPermanent(errors.New("smtp: timeout")))
	assert.ErrorContains(t, err, "recipient rejected: 550")
}

func TestReasonDetailIsBoundedAndNeverEmpty(t *testing.T) {
	t.Parallel()

	assert.Equal(t, ReasonTransient, Reason{Class: ReasonTransient}.detail(), "an empty detail falls back to the class")
	assert.Len(t, Reason{Class: ReasonTransient, Detail: strings.Repeat("x", 5000)}.detail(), maxErrorDetail)
	assert.Equal(t, ReasonNoRecourse, Reason{}.class())
}
