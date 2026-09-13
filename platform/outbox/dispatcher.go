package outbox

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/platform/metrics"
)

// Defaults for the delivery loop. They are deliberately modest: the job runs
// every few seconds, so a batch is a sip, not a gulp, and a message that cannot
// be delivered spends about three hours of increasingly patient retries before
// it is given up on.
const (
	DefaultBatchSize   = 50
	DefaultClaimLease  = 2 * time.Minute
	DefaultRetention   = 30 * 24 * time.Hour
	DefaultBaseDelay   = time.Minute
	DefaultMaxDelay    = time.Hour
	DefaultMaxAttempts = 8
	backoffFactor      = 2.0

	// DefaultSendTimeout bounds ONE transport call. It is the difference
	// between "the provider is slow" and "the provider has stopped answering",
	// and it must be short enough that a pass can absorb a hang and carry on —
	// a mail transport's own timeout is not a substitute, because a transport
	// that never returns is precisely the case being defended against.
	DefaultSendTimeout = 10 * time.Second
	// DefaultBatchBudget bounds one whole pass. The scheduler also bounds the
	// job (its Timeout, or its Interval when none is set) and whichever comes
	// first wins; this default is what applies when the caller passes a context
	// with no deadline at all.
	DefaultBatchBudget = time.Minute

	// settleTimeout bounds the write that records a delivery's outcome. It runs
	// on a context detached from shutdown (see deliver), so it needs a deadline
	// of its own.
	settleTimeout = 10 * time.Second
)

// Metric labels. client_name is the subsystem, operation is the outcome, so the
// existing client_request_duration_seconds histogram yields both the COUNT of
// each outcome and the time the transport took, with no new instrument.
const (
	metricClient = "outbox"

	opClaim     = "claim"
	opDelivered = "delivered"
	opRetried   = "retried"
	opDead      = "dead"
	opTimedOut  = "timed_out"
	opReleased  = "released"
	opSettleErr = "settle.failed"
	opPrune     = "prune"
)

// MessageStore is the persistence the delivery loop needs. *Store implements
// it; tests substitute a fake.
type MessageStore interface {
	ClaimDue(ctx context.Context, limit int, lease time.Duration) ([]Message, error)
	ReleaseClaim(ctx context.Context, msgs []Message) error
	MarkSent(ctx context.Context, m Message) error
	Reschedule(ctx context.Context, m Message, at time.Time, r Reason) error
	MarkDead(ctx context.Context, m Message, r Reason) error
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

// Backoff is the retry schedule: Base * Factor^(attempts-1), capped at Max, up
// to MaxAttempts attempts in total.
type Backoff struct {
	Base        time.Duration
	Max         time.Duration
	MaxAttempts int
	// Jitter spreads a retry so that a batch failed by one provider outage does
	// not return as one thundering batch. Nil means no jitter (tests).
	Jitter func(time.Duration) time.Duration
}

// DefaultBackoff is the production schedule: 1m, 2m, 4m, 8m, 16m, 32m, 1h, 1h
// with ±10% jitter, then dead.
func DefaultBackoff() Backoff {
	return Backoff{
		Base:        DefaultBaseDelay,
		Max:         DefaultMaxDelay,
		MaxAttempts: DefaultMaxAttempts,
		Jitter:      jitter10,
	}
}

func (b Backoff) withDefaults() Backoff {
	if b.Base <= 0 {
		b.Base = DefaultBaseDelay
	}
	if b.Max <= 0 {
		b.Max = DefaultMaxDelay
	}
	if b.MaxAttempts <= 0 {
		b.MaxAttempts = DefaultMaxAttempts
	}
	return b
}

// Delay is how long to wait after the given number of attempts (1 = the first
// attempt just failed). It is monotonic, capped, and never negative — the
// exponent is computed in float64 and clamped before it can overflow an int64.
func (b Backoff) Delay(attempts int) time.Duration {
	b = b.withDefaults()
	if attempts < 1 {
		attempts = 1
	}

	grown := float64(b.Base) * math.Pow(backoffFactor, float64(attempts-1))
	delay := b.Max
	if grown < float64(b.Max) {
		delay = time.Duration(grown)
	}
	if b.Jitter != nil {
		delay = b.Jitter(delay)
	}
	if delay < 0 {
		delay = 0
	}
	return delay
}

// jitter10 spreads a delay by ±10%.
func jitter10(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	spread := d / 5 // 20% wide, centred by the -10% below.
	//nolint:gosec // scheduling jitter, not a secret: math/rand is the right tool.
	return d - spread/2 + time.Duration(rand.N(int64(spread)+1))
}

// Settings tune the delivery job.
type Settings struct {
	// BatchSize bounds one pass.
	BatchSize int
	// ClaimLease is how long a claimed message is invisible to other runners.
	// It must comfortably exceed the transport's own timeout, or a slow send
	// becomes a duplicate send.
	ClaimLease time.Duration
	// Retention is how long a settled message is kept before the prune job
	// removes it.
	Retention time.Duration
	// SendTimeout is how long ONE message may occupy the transport. A provider
	// that has not answered within it has failed THAT message's attempt — the
	// pass moves on to the next message rather than spending the whole window
	// on one unanswered call.
	SendTimeout time.Duration
	// BatchBudget is how long one whole pass may spend sending. When it runs
	// out, the messages the pass never reached are handed back to the queue
	// unspent (see Dispatcher.DeliverDue), so they are the next runner's work
	// rather than attempts charged to a transport that never saw them.
	BatchBudget time.Duration
	// Backoff is the retry schedule.
	Backoff Backoff
}

func (s Settings) withDefaults() Settings {
	if s.BatchSize <= 0 {
		s.BatchSize = DefaultBatchSize
	}
	if s.ClaimLease <= 0 {
		s.ClaimLease = DefaultClaimLease
	}
	if s.Retention <= 0 {
		s.Retention = DefaultRetention
	}
	if s.SendTimeout <= 0 {
		s.SendTimeout = DefaultSendTimeout
	}
	if s.BatchBudget <= 0 {
		s.BatchBudget = DefaultBatchBudget
	}
	// A pass must finish, settle included, inside the lease it claimed under.
	// A pass that outlived its own claim would be writing outcomes for rows a
	// second runner had already taken — the one race the lease exists to
	// prevent.
	if latest := s.ClaimLease - settleTimeout; latest > 0 && s.BatchBudget > latest {
		s.BatchBudget = latest
	}
	// One send may never be bigger than the pass that has to contain it, or no
	// message would ever be started at all.
	if s.SendTimeout > s.BatchBudget {
		s.SendTimeout = s.BatchBudget
	}
	s.Backoff = s.Backoff.withDefaults()
	return s
}

// Dispatcher is the delivery loop: claim a batch, send each message, record
// what happened. Its two methods are the scheduler jobs.
type Dispatcher struct {
	store    MessageStore
	sender   Sender
	recorder metrics.ClientRecorder
	settings Settings
	seal     *Seal
	now      func() time.Time
}

func NewDispatcher(store MessageStore, sender Sender, recorder metrics.ClientRecorder, settings Settings) *Dispatcher {
	return &Dispatcher{
		store:    store,
		sender:   sender,
		recorder: recorder,
		settings: settings.withDefaults(),
		now:      time.Now,
	}
}

// WithSeal gives the loop the key that opens a stored payload (seal.go). It
// must be the same seal the Queue writes with; without it a sealed message
// cannot be rendered and is reported as unreadable rather than sent.
func (d *Dispatcher) WithSeal(s *Seal) *Dispatcher {
	d.seal = s
	return d
}

// DeliverDue is the scheduler job: one pass over the messages that are due.
//
// A send that fails is NOT an error of this job — it is the outcome the retry
// schedule exists for, and it is counted, not returned. What this job reports
// as an error is an inability to do its own work: the claim failed, or a
// message's fate could not be written down. That distinction keeps the
// scheduler's failure counter meaningful: it rises when the queue itself is
// broken, not when a mail server is having a bad afternoon.
//
// TIME IS THE SCARCE RESOURCE, and it is rationed twice. Each message gets at
// most SendTimeout of the transport, so one unanswered call costs one message's
// window rather than the pass; and the pass itself stops starting sends at
// BatchBudget (or at the caller's deadline, whichever is sooner). Every claimed
// message the pass did not reach is then handed BACK to the queue — its lease
// dropped and the attempt the claim charged refunded — because an attempt the
// transport never saw is not an attempt. That is what keeps one hung provider
// from spending the whole batch's budget, and the whole batch's attempt budget,
// on a message it was never going to deliver.
func (d *Dispatcher) DeliverDue(ctx context.Context) error {
	start := d.now()
	msgs, err := d.store.ClaimDue(ctx, d.settings.BatchSize, d.settings.ClaimLease)
	d.observe(opClaim, d.now().Sub(start))
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil
	}

	deadline := d.batchDeadline(ctx, d.now())

	// rest is what the pass has not accounted for yet; it shrinks by one as
	// each message's fate is written down.
	rest := msgs
	var unsettled int
	for len(rest) > 0 {
		if ctx.Err() != nil {
			break // shutdown: what is left goes back to the queue below.
		}
		window, whole := d.sendWindow(deadline, len(rest) == len(msgs))
		if window <= 0 {
			break // out of budget.
		}

		outcome, err := d.deliver(ctx, rest[0], window, whole)
		if err != nil {
			unsettled++
			d.observe(opSettleErr, 0)
			logger.Log.Error("Outbox message could not be settled",
				logger.String("message", rest[0].ID),
				logger.String("template", rest[0].Template),
				logger.Error(err),
			)
		}
		if outcome == outcomeUnclaimed {
			break // nothing was charged to this one either; release it with the rest.
		}
		rest = rest[1:]
	}

	var problems []error
	if unsettled > 0 {
		problems = append(problems, fmt.Errorf("outbox: %d of %d claimed messages could not be settled", unsettled, len(msgs)))
	}
	if err := d.releaseClaims(ctx, rest, len(rest) == len(msgs)); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

// batchDeadline is when this pass must stop starting sends: its own budget, or
// the caller's deadline when that comes first. It is expressed on the
// dispatcher's clock, so a test clock governs the budget exactly as the wall
// clock governs production.
func (d *Dispatcher) batchDeadline(ctx context.Context, now time.Time) time.Time {
	budget := d.settings.BatchBudget
	if caller, ok := ctx.Deadline(); ok {
		if remaining := time.Until(caller); remaining < budget {
			budget = remaining
		}
	}
	return now.Add(budget)
}

// sendWindow is how long the next message may hold the transport, and whether
// that is the WHOLE window it is entitled to.
//
// A message is started only if the pass can give it its full SendTimeout —
// starting a send the pass would have to cut short is how an attempt gets
// charged to a provider that was never given a fair chance to answer. The one
// exception is the first message of a pass: it gets whatever is left even if
// that is less than a full window, because a budget too small to fit one send
// would otherwise stall the queue completely and in silence. A message cut
// short by such a partial window is never charged for it (see deliver).
func (d *Dispatcher) sendWindow(deadline time.Time, first bool) (window time.Duration, whole bool) {
	remaining := deadline.Sub(d.now())
	switch {
	case remaining >= d.settings.SendTimeout:
		return d.settings.SendTimeout, true
	case first && remaining > 0:
		return remaining, false
	default:
		return 0, false
	}
}

// releaseClaims hands the messages this pass never delivered back to the queue.
//
// Like settling, it runs on a context detached from shutdown: the whole point
// is that a runner going down mid-batch leaves every row in a state the next
// runner can pick up AT ONCE, rather than a batch of rows that look attempted
// and stay invisible until their lease expires.
func (d *Dispatcher) releaseClaims(ctx context.Context, msgs []Message, none bool) error {
	if len(msgs) == 0 {
		return nil
	}

	reason := "the pass ran out of budget"
	switch {
	case ctx.Err() != nil:
		reason = "the runner is shutting down"
	case none:
		// Not one message fitted: the pass budget is smaller than a single
		// send. Nothing will ever be delivered until that is changed, so say so
		// rather than churning quietly.
		reason = "the pass budget is too small to deliver anything"
	}
	logger.Log.Warn("Outbox messages returned to the queue unattempted",
		logger.Int("messages", len(msgs)),
		logger.String("reason", reason),
	)

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()

	start := d.now()
	err := d.store.ReleaseClaim(writeCtx, msgs)
	d.observe(opReleased, d.now().Sub(start))
	if err != nil {
		return fmt.Errorf("outbox: return %d unattempted messages to the queue: %w", len(msgs), err)
	}
	return nil
}

// deliveryOutcome says what became of a claim.
type deliveryOutcome int

const (
	// outcomeSettled: the message's fate was decided — written down, or the
	// write failed and is reported. Either way the attempt was real and the
	// claim is spent.
	outcomeSettled deliveryOutcome = iota
	// outcomeUnclaimed: the transport was never given a fair chance (shutdown,
	// or a window the pass had to clip), so nothing was written down and the
	// claim must go back to the queue, attempt and all.
	outcomeUnclaimed
)

// deliver sends one message within window and writes down what happened to it.
// The returned error means the OUTCOME could not be recorded — a send failure
// is a recorded outcome, not an error.
//
// A send that never answers is the case this function exists to make
// survivable. window is enforced on the context handed to the transport, so
// cancellation reaches the provider call itself rather than being noticed
// between messages; a Sender is required to honour it (both shipped transports
// do — they set socket deadlines from the context). Silence for a whole window
// is then treated as what it is: a FAILED ATTEMPT, retried on the backoff and
// dead-lettered when the attempt budget runs out. Nothing about a hung provider
// is exempt from the state machine — a message must always end somewhere a
// human can explain.
func (d *Dispatcher) deliver(ctx context.Context, m Message, window time.Duration, whole bool) (deliveryOutcome, error) {
	// The column holds a SEALED payload. Open it here, one message at a time,
	// so the cleartext exists in this frame and nowhere else — not in the
	// database, not in the claim's result set for the whole batch's lifetime,
	// and (the ADR-0015 gate enforces this) not in a log line.
	plain, err := d.seal.Open(m.Payload)
	if err != nil {
		return d.settleUnreadable(ctx, m, err)
	}
	m.Payload = plain

	sendCtx, cancelSend := context.WithTimeout(ctx, window)
	start := d.now()
	err = d.sender.Send(sendCtx, m)
	elapsed := d.now().Sub(start)
	timedOut := err != nil && errors.Is(sendCtx.Err(), context.DeadlineExceeded)
	cancelSend()

	class := ReasonTransient
	switch {
	case err != nil && ctx.Err() != nil:
		// The send was cut short by shutdown, not by the transport. Charge it
		// to nobody: the claim goes back and the message is tried again as it
		// stands.
		return outcomeUnclaimed, nil

	case timedOut && !whole:
		// The pass clipped this message's window, so the silence is ours and
		// not the provider's. Hand the claim back unspent.
		return outcomeUnclaimed, nil

	case timedOut:
		// The provider had the whole window and said nothing.
		class = ReasonTimeout
		err = fmt.Errorf("delivery timed out after %s: %w", window, err)
		d.observe(opTimedOut, elapsed)
	}

	// Settling must not be cancellable. If shutdown could interrupt the write
	// that says "this one was sent", every shutdown mid-batch would manufacture
	// duplicates on the next pass — the one duplicate the design can actually
	// avoid. So the outcome is recorded on a detached, briefly bounded context.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()

	switch {
	case err == nil:
		d.observe(opDelivered, elapsed)
		return outcomeSettled, d.store.MarkSent(ctx, m)

	case IsPermanent(err):
		d.observe(opDead, elapsed)
		logger.Log.Warn("Outbox message permanently rejected",
			logger.String("message", m.ID),
			logger.String("template", m.Template),
			logger.Error(err),
		)
		return outcomeSettled, d.store.MarkDead(ctx, m, Reason{Class: ReasonPermanent, Detail: err.Error()})

	case m.Attempts >= d.settings.Backoff.MaxAttempts:
		// Attempts were charged by the claim, so this count includes the
		// attempt that just failed: the budget is spent.
		d.observe(opDead, elapsed)
		logger.Log.Warn("Outbox message given up after exhausting its attempts",
			logger.String("message", m.ID),
			logger.String("template", m.Template),
			logger.Int("attempts", m.Attempts),
			logger.Error(err),
		)
		return outcomeSettled, d.store.MarkDead(ctx, m, Reason{Class: ReasonExhausted, Detail: err.Error()})

	default:
		delay := d.settings.Backoff.Delay(m.Attempts)
		d.observe(opRetried, elapsed)
		logger.Log.Info("Outbox message delivery failed; retrying later",
			logger.String("message", m.ID),
			logger.String("template", m.Template),
			logger.Int("attempts", m.Attempts),
			logger.String("retryIn", delay.String()),
			logger.String("failure", class),
			logger.Error(err),
		)
		return outcomeSettled, d.store.Reschedule(ctx, m, d.now().Add(delay), Reason{Class: class, Detail: err.Error()})
	}
}

// settleUnreadable writes down a message whose sealed payload this build cannot
// open, and it does so on the RETRY schedule rather than as a permanent
// rejection.
//
// The distinction matters and is the opposite of the renderer's. A payload the
// renderer refuses is a property of the ROW — nothing an operator does will
// make it decode, so the message is dead on the first return. A payload that
// will not OPEN is a property of the DEPLOYMENT: the process booted without the
// encryption key, or with the wrong one, or with an envelope from a newer
// build. All three are fixable in minutes and none of them is the message's
// fault, so giving up on the first pass would destroy every queued
// notification — irreversibly, since a settled row cannot be re-opened — over a
// misconfiguration. The attempt budget still ends it: an operator who does not
// fix the key has, as for any other stuck message, a few hours before the queue
// dead-letters with an audit entry per tenant.
//
// It is logged at ERROR for the same reason: one of these lines means the
// product cannot send ANY mail, which no metric on its own would say.
func (d *Dispatcher) settleUnreadable(ctx context.Context, m Message, cause error) (deliveryOutcome, error) {
	// Settling is detached from shutdown, exactly as in deliver.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()

	reason := Reason{Class: ReasonUnreadable, Detail: cause.Error()}
	if m.Attempts >= d.settings.Backoff.MaxAttempts {
		d.observe(opDead, 0)
		logger.Log.Error("Outbox message payload could not be opened; attempts exhausted",
			logger.String("message", m.ID),
			logger.String("template", m.Template),
			logger.Int("attempts", m.Attempts),
			logger.String("remedy", "check AUTH_ENCRYPTION_KEY matches the key the message was queued under"),
			logger.Error(cause),
		)
		return outcomeSettled, d.store.MarkDead(ctx, m, reason)
	}

	delay := d.settings.Backoff.Delay(m.Attempts)
	d.observe(opRetried, 0)
	logger.Log.Error("Outbox message payload could not be opened; retrying later",
		logger.String("message", m.ID),
		logger.String("template", m.Template),
		logger.Int("attempts", m.Attempts),
		logger.String("retryIn", delay.String()),
		logger.String("remedy", "check AUTH_ENCRYPTION_KEY matches the key the message was queued under"),
		logger.Error(cause),
	)
	return outcomeSettled, d.store.Reschedule(ctx, m, d.now().Add(delay), reason)
}

// PruneSettled is the second scheduler job: clear out messages settled longer
// ago than the retention window, so the queue stays the small, drained table
// the schema assumes — and so a delivered message's recipient address stops
// existing (ADR-0007).
func (d *Dispatcher) PruneSettled(ctx context.Context) error {
	start := d.now()
	removed, err := d.store.Prune(ctx, d.settings.Retention)
	d.observe(opPrune, d.now().Sub(start))
	if err != nil {
		return err
	}
	if removed > 0 {
		logger.Log.Info("Outbox pruned settled messages", logger.Int("removed", int(removed)))
	}
	return nil
}

func (d *Dispatcher) observe(operation string, elapsed time.Duration) {
	if d.recorder == nil {
		return
	}
	d.recorder.ObserveDuration(metricClient, operation, elapsed)
}
