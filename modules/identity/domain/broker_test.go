package domain

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// brokerOp is one IdentityBroker operation flattened to a single shape, so the
// table can assert the same contract over all three.
type brokerOp struct {
	name string
	call func(IdentityBroker) (any, error)
	zero any // what the operation returns alongside the refusal
}

func brokerOps() []brokerOp {
	return []brokerOp{
		{
			name: "StartSSO",
			call: func(b IdentityBroker) (any, error) {
				url, err := b.StartSSO(context.Background(), "acme-okta")
				return url, err
			},
			zero: "",
		},
		{
			name: "StartRecovery",
			call: func(b IdentityBroker) (any, error) {
				url, err := b.StartRecovery(context.Background(), "cfo@acme.example")
				return url, err
			},
			zero: "",
		},
		{
			name: "CompleteRecovery",
			call: func(b IdentityBroker) (any, error) {
				id, err := b.CompleteRecovery(context.Background(), "recovery-token")
				return id, err
			},
			zero: (*RecoveredIdentity)(nil),
		},
	}
}

// Every operation of the broker an unconfigured edition runs refuses with
// ErrNoIdentityProvider and does nothing else: no other error, no half-built
// result, and no state that makes the second call differ from the first.
func TestNotConfiguredBroker_RefusesEveryOperation(t *testing.T) {
	t.Parallel()

	var broker IdentityBroker = NotConfiguredBroker{}

	for _, op := range brokerOps() {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()

			got, err := op.call(broker)
			require.Error(t, err)
			require.ErrorIs(t, err, ErrNoIdentityProvider)
			assert.Equal(t, MsgNoIdentityProvider, err.Error(),
				"the refusal says only how the server is configured — never anything about the subject")
			assert.Equal(t, op.zero, got, "a refused operation returns no result at all")

			// Stateless: it holds nothing, so a retry or a concurrent caller
			// gets the identical answer and no partial work accumulates.
			got2, err2 := op.call(broker)
			require.ErrorIs(t, err2, ErrNoIdentityProvider)
			assert.Equal(t, op.zero, got2)
		})
	}
}

// The refusal is one typed value, so a route can answer the condition uniformly
// instead of each caller inventing a message: it classifies as not-found (the
// duck-typed interface delivery/httpkit/httperr.Classify looks for → 404 with
// MsgNoIdentityProvider) and as nothing else, so it can never fall through to
// the 500 an unrecognised error earns. Asserted structurally, so the domain
// keeps no dependency on the transport package.
func TestErrNoIdentityProvider_ClassifiesAsOneKnownRefusal(t *testing.T) {
	t.Parallel()

	type notFound interface{ IsNotFound() bool }

	var nf notFound
	require.ErrorAs(t, ErrNoIdentityProvider, &nf)
	assert.True(t, nf.IsNotFound())

	// Not also a validation / conflict / forbidden / unauthorized error: the
	// classification must be unambiguous, whatever order Classify tries.
	type validation interface{ IsValidation() bool }
	type conflict interface{ IsConflict() bool }
	type forbidden interface{ IsForbidden() bool }
	type unauthorized interface{ IsUnauthorized() bool }

	assert.NotErrorAs(t, ErrNoIdentityProvider, new(validation))
	assert.NotErrorAs(t, ErrNoIdentityProvider, new(conflict))
	assert.NotErrorAs(t, ErrNoIdentityProvider, new(forbidden))
	assert.NotErrorAs(t, ErrNoIdentityProvider, new(unauthorized))

	// A caller that adds context keeps both properties: errors.Is still matches
	// and the classifier still sees through the wrap.
	wrapped := fmt.Errorf("start recovery: %w", ErrNoIdentityProvider)
	require.ErrorIs(t, wrapped, ErrNoIdentityProvider)
	require.ErrorAs(t, wrapped, &nf)
}

// "No broker was configured" is not "the broker failed". The first is the
// steady state of every edition today and must not be paged or retried as an
// incident; the second is a real dependency failure. Nothing but the sentinel
// satisfies the check — a same-sentence lookalike included, which is why
// callers compare with errors.Is and never with the message.
func TestErrNoIdentityProvider_DistinguishableFromGenuineFailure(t *testing.T) {
	t.Parallel()

	genuine := errors.New("broker: 503 from the provider endpoint")
	assert.NotErrorIs(t, genuine, ErrNoIdentityProvider)
	assert.NotErrorIs(t, fmt.Errorf("complete recovery: %w", genuine), ErrNoIdentityProvider)

	lookalike := errors.New(MsgNoIdentityProvider)
	require.Equal(t, ErrNoIdentityProvider.Error(), lookalike.Error(), "same sentence")
	assert.NotErrorIs(t, lookalike, ErrNoIdentityProvider, "…and still not the sentinel")

	// A genuine failure from a CONFIGURED broker is not the sentinel either,
	// even on the same operation that the unconfigured one refuses.
	_, err := failingBroker{}.CompleteRecovery(context.Background(), "recovery-token")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoIdentityProvider)
}

// A configured broker returns a proof and no error on the recovery path. This
// pins the shape the seam promises a real provider (WorkOS / OSS / direct OIDC)
// will be written against: a verified address, never a session or a credential
// — the app mints the session and revokes the old ones itself.
func TestIdentityBroker_ConfiguredBrokerReturnsAProofAndNoSession(t *testing.T) {
	t.Parallel()

	var broker IdentityBroker = stubBroker{}

	url, err := broker.StartRecovery(context.Background(), "cfo@acme.example")
	require.NoError(t, err)
	assert.Equal(t, "https://broker.example/recover", url)

	id, err := broker.CompleteRecovery(context.Background(), "recovery-token")
	require.NoError(t, err)
	require.NotNil(t, id)
	assert.Equal(t, "cfo@acme.example", id.Email)
	assert.Equal(t, "user_01H", id.Subject)
}

// stubBroker stands in for a configured provider (test-only).
type stubBroker struct{}

var _ IdentityBroker = stubBroker{}

func (stubBroker) StartSSO(context.Context, string) (string, error) {
	return "https://broker.example/sso", nil
}

func (stubBroker) StartRecovery(context.Context, string) (string, error) {
	return "https://broker.example/recover", nil
}

func (stubBroker) CompleteRecovery(context.Context, string) (*RecoveredIdentity, error) {
	return &RecoveredIdentity{Email: "cfo@acme.example", Subject: "user_01H"}, nil
}

// failingBroker stands in for a provider that was reached and failed
// (test-only).
type failingBroker struct{}

var _ IdentityBroker = failingBroker{}

var errBrokerUnreachable = errors.New("broker: dial tcp: connection refused")

func (failingBroker) StartSSO(context.Context, string) (string, error) {
	return "", errBrokerUnreachable
}

func (failingBroker) StartRecovery(context.Context, string) (string, error) {
	return "", errBrokerUnreachable
}

func (failingBroker) CompleteRecovery(context.Context, string) (*RecoveredIdentity, error) {
	return nil, errBrokerUnreachable
}
