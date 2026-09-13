package outbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What is actually in the column, read back the way anybody with database
// access would read it. These are the assertions the unit tests cannot make:
// the seal is only worth something if the ROW is ciphertext, and the clearing
// is only worth something if the state trigger allows it.
//
//	TEST_DATABASE_URL=postgres://zentax_test_app:zentax-test-app@localhost:5433/zentax_test?sslmode=disable \
//	  go test ./platform/outbox/...

// payloadOf reads the payload column as stored — no unsealing, no Go types.
func (h *harness) payloadOf(t *testing.T, id string) string {
	t.Helper()
	var payload string
	require.NoError(t, h.asTenant(h.tenant, h.user, func(ctx context.Context) error {
		return h.exec.GetContext(ctx, &payload,
			`SELECT payload::text FROM outbox_messages WHERE id = $1`, id)
	}))
	return payload
}

// invite is the message that made the seal necessary.
func invite() NewMessage {
	return NewMessage{
		Template:  "member.invited",
		Recipient: "jane.doe@acme.test",
		Payload: map[string]any{
			"recipientName": "Jane Doe",
			"inviteToken":   "zti_Hf_ttLMtYrwHQjmaltCYUy8q06AGUVwjmfXan3yDkAI",
			"expiresAt":     "2026-09-20T00:00:00Z",
		},
	}
}

func TestAQueuedCredentialIsCiphertextInTheColumn(t *testing.T) {
	h := newHarness(t)
	h.queue.WithSeal(testSeal(t))

	r := h.enqueue(t, invite())
	stored := h.payloadOf(t, r.ID)

	assert.NotContains(t, stored, "zti_",
		"invite_tokens keeps only a SHA-256 so a leaked table cannot be replayed; the outbox must not undo that")
	assert.NotContains(t, stored, "Jane Doe")
	assert.True(t, IsSealed([]byte(stored)), "the column holds an envelope: %s", stored)

	// And it is still the same message: the runner can open exactly what was
	// queued.
	claimed, err := h.store.ClaimDue(context.Background(), 100, time.Minute)
	require.NoError(t, err)
	opened, err := testSeal(t).Open(find(t, claimed, r.ID).Payload)
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"recipientName":"Jane Doe","inviteToken":"zti_Hf_ttLMtYrwHQjmaltCYUy8q06AGUVwjmfXan3yDkAI","expiresAt":"2026-09-20T00:00:00Z"}`,
		string(opened))
}

// The second half: sealing bounds WHO can read it, settling bounds HOW LONG it
// exists. A delivered invite leaves nothing to open at all.
func TestSettlingClearsTheCredentialFromTheRow(t *testing.T) {
	h := newHarness(t)
	h.queue.WithSeal(testSeal(t))
	ctx := context.Background()

	r := h.enqueue(t, invite())
	claimed, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	require.NoError(t, h.store.MarkSent(ctx, find(t, claimed, r.ID)))

	status, _, _ := h.statusOf(t, r.ID)
	assert.Equal(t, string(StatusSent), status)
	assert.JSONEq(t, `{}`, h.payloadOf(t, r.ID),
		"a delivered message keeps nothing to be replayed, sealed or not")
}

func TestGivingUpAlsoClearsTheCredentialFromTheRow(t *testing.T) {
	h := newHarness(t)
	h.queue.WithSeal(testSeal(t))
	ctx := context.Background()

	r := h.enqueue(t, invite())
	claimed, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	require.NoError(t, h.store.MarkDead(ctx, find(t, claimed, r.ID),
		Reason{Class: ReasonPermanent, Detail: "550 5.1.1 no such user"}))

	status, _, lastErr := h.statusOf(t, r.ID)
	assert.Equal(t, string(StatusDead), status)
	assert.JSONEq(t, `{}`, h.payloadOf(t, r.ID))

	// What an operator is left with is the diagnosis, not the content.
	require.NotNil(t, lastErr)
	assert.Contains(t, *lastErr, "550")
}

// The whole path against live Postgres, with the real Store and the real
// Dispatcher: queue an invite, run one delivery pass, and check that the only
// place the credential was ever in the clear is the transport call.
func TestOneDeliveryPassOpensSealsAndClearsTheCredential(t *testing.T) {
	h := newHarness(t)
	seal := testSeal(t)
	h.queue.WithSeal(seal)

	r := h.enqueue(t, invite())
	require.True(t, IsSealed([]byte(h.payloadOf(t, r.ID))))

	sender := &fakeSender{}
	d := NewDispatcher(h.store, sender, newSpyRecorder(), Settings{BatchSize: 100}).WithSeal(seal)
	require.NoError(t, d.DeliverDue(context.Background()))

	var delivered *Message
	for i := range sender.seen {
		if sender.seen[i].ID == r.ID {
			delivered = &sender.seen[i]
		}
	}
	require.NotNil(t, delivered, "the pass must have reached this message")
	assert.Contains(t, string(delivered.Payload), "zti_",
		"the transport is the one place the credential exists in the clear")

	status, _, _ := h.statusOf(t, r.ID)
	assert.Equal(t, string(StatusSent), status)
	assert.JSONEq(t, `{}`, h.payloadOf(t, r.ID), "and it is gone from the row the moment the mail goes out")
}

// A retry is NOT a settlement: a message still owed keeps what it needs to be
// rendered when the transport comes back.
func TestAReschedulingKeepsThePayload(t *testing.T) {
	h := newHarness(t)
	h.queue.WithSeal(testSeal(t))
	ctx := context.Background()

	r := h.enqueue(t, invite())
	claimed, err := h.store.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	require.NoError(t, h.store.Reschedule(ctx, find(t, claimed, r.ID),
		time.Now().UTC().Add(time.Minute), Reason{Class: ReasonTransient, Detail: "421 try later"}))

	assert.True(t, IsSealed([]byte(h.payloadOf(t, r.ID))),
		"a message that is still owed must still be renderable")
}

// The guard was relaxed by exactly one clause. Everything else it refused, it
// still refuses — including the interesting one: overwriting the payload with
// content of the attacker's choosing rather than clearing it.
func TestThePayloadMayOnlyEverBeCleared(t *testing.T) {
	h := newHarness(t)
	h.queue.WithSeal(testSeal(t))
	ctx := context.Background()

	r := h.enqueue(t, invite())

	err := h.store.withinRunnerTx(ctx, func(ctx context.Context) error {
		_, err := h.exec.ExecContext(ctx,
			`UPDATE outbox_messages SET payload = '{"inviteToken":"zti_forged"}'::jsonb WHERE id = $1`, r.ID)
		return err
	})
	require.Error(t, err, "a pending message's content is frozen")

	// Nor may it be emptied while the message is still pending — that would be
	// a way to make an owed message unrenderable and lose it silently.
	err = h.store.withinRunnerTx(ctx, func(ctx context.Context) error {
		_, err := h.exec.ExecContext(ctx,
			`UPDATE outbox_messages SET payload = '{}'::jsonb WHERE id = $1`, r.ID)
		return err
	})
	require.Error(t, err, "clearing is part of settling, not something on its own")
}
