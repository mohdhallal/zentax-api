package outbox

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testKey is the development AES-256 key (config.DevelopmentEncryptionKey,
// decoded). It is spelled out rather than imported so this package stays a
// leaf — platform must not depend on config.
var testKey = []byte("zentax-dev-encryption-key-32byte")

// otherKey stands in for a rotated or mismatched key.
var otherKey = []byte("a-completely-different-32-byte!!")

// inviteVariables is the payload that made this file necessary: a live
// single-use credential and the invitee's name.
const inviteVariables = `{"recipientName":"Jane Doe","inviteToken":"zti_Hf_ttLMtYrwHQjmaltCYUy8q06AGUVwjmfXan3yDkAI","expiresAt":"2026-09-20T00:00:00Z"}`

func testSeal(t *testing.T) *Seal {
	t.Helper()
	s, err := NewSeal(testKey)
	require.NoError(t, err)
	return s
}

// --- the seal itself --------------------------------------------------------

func TestSealHidesTheCredentialAndOpensBackToIt(t *testing.T) {
	t.Parallel()
	s := testSeal(t)

	stored, err := s.Seal([]byte(inviteVariables))
	require.NoError(t, err)

	// What lands in the column: an envelope, and nowhere in it the token, the
	// person's name, or any other value of the payload.
	assert.True(t, IsSealed(stored), "the stored payload must be an envelope: %s", stored)
	assert.NotContains(t, string(stored), "zti_", "the credential must not be at rest in cleartext")
	assert.NotContains(t, string(stored), "Jane Doe")
	assert.NotContains(t, string(stored), "inviteToken")
	assert.True(t, json.Valid(stored), "the column is JSONB, so the envelope must be valid JSON")

	opened, err := s.Open(stored)
	require.NoError(t, err)
	assert.JSONEq(t, inviteVariables, string(opened), "the renderer must see exactly what was queued")
}

// The composition root builds one seal for the queue and one for the delivery
// loop (the invite producer is wired in New, the runner in newScheduler), so
// what has to match is the KEY, not the instance.
func TestASecondSealOnTheSameKeyOpensTheFirstsEnvelope(t *testing.T) {
	t.Parallel()

	producer, err := NewSeal(testKey)
	require.NoError(t, err)
	runner, err := NewSeal(append([]byte(nil), testKey...))
	require.NoError(t, err)

	stored, err := producer.Seal([]byte(inviteVariables))
	require.NoError(t, err)
	opened, err := runner.Open(stored)
	require.NoError(t, err)
	assert.JSONEq(t, inviteVariables, string(opened))
}

func TestSealIsNotDeterministic(t *testing.T) {
	t.Parallel()
	s := testSeal(t)

	first, err := s.Seal([]byte(inviteVariables))
	require.NoError(t, err)
	second, err := s.Seal([]byte(inviteVariables))
	require.NoError(t, err)

	assert.False(t, bytes.Equal(first, second),
		"a fresh nonce per message: two invites with the same content must not look alike at rest")
}

// A queue that already has rows in it must keep working across the deploy that
// introduces the seal.
func TestOpenPassesACleartextPayloadThrough(t *testing.T) {
	t.Parallel()
	s := testSeal(t)

	legacy := []byte(`{"taskId":"t-1","dueDate":"2026-10-15"}`)
	opened, err := s.Open(legacy)
	require.NoError(t, err)
	assert.JSONEq(t, string(legacy), string(opened))

	// And a settled row's cleared payload is not an envelope either.
	assert.False(t, IsSealed([]byte(`{}`)))
	assert.False(t, IsSealed(nil))
}

func TestOpenRefusesAnEnvelopeItCannotRead(t *testing.T) {
	t.Parallel()

	sealed, err := testSeal(t).Seal([]byte(inviteVariables))
	require.NoError(t, err)

	rotated, err := NewSeal(otherKey)
	require.NoError(t, err)
	_, err = rotated.Open(sealed)
	require.Error(t, err, "the wrong key must not silently produce a wrong payload")

	// No key at all is the keyless build meeting a sealed row: an error, not a
	// cleartext guess.
	var none *Seal
	_, err = none.Open(sealed)
	require.ErrorContains(t, err, "no encryption key is configured")

	// A version this build does not know is refused rather than misread.
	future, err := json.Marshal(envelope{EncV: sealVersion + 1, Enc: "irrelevant"})
	require.NoError(t, err)
	_, err = testSeal(t).Open(future)
	require.ErrorContains(t, err, "version")
}

func TestNilSealStoresWhatItIsGiven(t *testing.T) {
	t.Parallel()

	var none *Seal
	stored, err := none.Seal([]byte(inviteVariables))
	require.NoError(t, err)
	assert.JSONEq(t, inviteVariables, string(stored),
		"a build with no key keeps working; SealFromKey is what says so in the log")
}

func TestNewSealRefusesAKeyThatIsNotAES256(t *testing.T) {
	t.Parallel()

	_, err := NewSeal([]byte("too short"))
	require.Error(t, err)
	assert.Nil(t, SealFromKey([]byte("too short")), "an unusable key gives no seal")
	assert.NotNil(t, SealFromKey(testKey))
}

// The key is copied in, so a caller reusing its buffer cannot change what the
// seal encrypts with half way through a process's life.
func TestSealKeepsItsOwnCopyOfTheKey(t *testing.T) {
	t.Parallel()

	key := append([]byte(nil), testKey...)
	s, err := NewSeal(key)
	require.NoError(t, err)
	sealed, err := s.Seal([]byte(inviteVariables))
	require.NoError(t, err)

	for i := range key {
		key[i] = 'x'
	}
	opened, err := s.Open(sealed)
	require.NoError(t, err)
	assert.JSONEq(t, inviteVariables, string(opened))
}

// --- the delivery path ------------------------------------------------------

// The property the whole change exists for: the transport sees the variables,
// the database never does.
func TestDeliveryOpensTheSealedPayloadForTheTransport(t *testing.T) {
	t.Parallel()

	s := testSeal(t)
	stored, err := s.Seal([]byte(inviteVariables))
	require.NoError(t, err)

	msg := pending("invite-1", 1)
	msg.Template = "member.invited"
	msg.Payload = stored

	store := &fakeStore{claim: []Message{msg}}
	sender := &fakeSender{}
	d := dispatcher(store, sender, newSpyRecorder()).WithSeal(s)

	require.NoError(t, d.DeliverDue(context.Background()))

	require.Equal(t, 1, sender.count())
	assert.JSONEq(t, inviteVariables, string(sender.seen[0].Payload),
		"the mail layer renders from the cleartext variables")
	assert.Equal(t, []string{"invite-1"}, store.sent)
}

// A payload this build cannot open is the deployment's fault, not the
// message's: it is retried, so an operator who fixes the key still gets the
// mail out, and only the attempt budget ends it.
func TestAnUnopenablePayloadIsRetriedNotDiscarded(t *testing.T) {
	t.Parallel()

	sealed, err := testSeal(t).Seal([]byte(inviteVariables))
	require.NoError(t, err)

	msg := pending("invite-1", 1)
	msg.Payload = sealed

	store := &fakeStore{claim: []Message{msg}}
	sender := &fakeSender{}
	rotated, err := NewSeal(otherKey)
	require.NoError(t, err)
	d := dispatcher(store, sender, newSpyRecorder()).WithSeal(rotated)

	require.NoError(t, d.DeliverDue(context.Background()))

	assert.Zero(t, sender.count(), "a message that cannot be opened must never reach a transport")
	assert.Empty(t, store.sent)
	assert.Empty(t, store.dead, "a key problem must not destroy the queue")
	require.Len(t, store.rescheduled, 1)
	assert.Equal(t, ReasonUnreadable, store.rescheduled[0].reason.Class)
	assert.Equal(t, fixedNow.Add(time.Minute), store.rescheduled[0].at)
}

func TestAnUnopenablePayloadIsDeadLetteredOnceTheBudgetIsSpent(t *testing.T) {
	t.Parallel()

	sealed, err := testSeal(t).Seal([]byte(inviteVariables))
	require.NoError(t, err)

	msg := pending("invite-1", 4) // MaxAttempts in the test dispatcher
	msg.Payload = sealed

	store := &fakeStore{claim: []Message{msg}}
	rotated, err := NewSeal(otherKey)
	require.NoError(t, err)
	d := dispatcher(store, &fakeSender{}, newSpyRecorder()).WithSeal(rotated)

	require.NoError(t, d.DeliverDue(context.Background()))

	require.Len(t, store.dead, 1)
	assert.Equal(t, ReasonUnreadable, store.dead[0].reason.Class)
	assert.Empty(t, store.rescheduled)
}

// A build with no seal still delivers the rows that predate one — the deploy
// order between the code and the key must not be a way to lose mail.
func TestASealessDispatcherStillDeliversACleartextPayload(t *testing.T) {
	t.Parallel()

	msg := pending("legacy-1", 1)
	msg.Payload = json.RawMessage(`{"taskId":"t-1"}`)

	store := &fakeStore{claim: []Message{msg}}
	sender := &fakeSender{}
	require.NoError(t, dispatcher(store, sender, newSpyRecorder()).DeliverDue(context.Background()))

	require.Equal(t, 1, sender.count())
	assert.JSONEq(t, `{"taskId":"t-1"}`, string(sender.seen[0].Payload))
	assert.Equal(t, []string{"legacy-1"}, store.sent)
}
