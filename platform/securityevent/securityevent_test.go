package securityevent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/app"
)

func testKey() []byte {
	key := make([]byte, keyLen)
	for i := range key {
		key[i] = byte(i * 7)
	}
	return key
}

// --- the subject digest ------------------------------------------------------

// The property the whole correlator rests on: one address is one digest,
// forever, however it was spelled. If it were not, an investigator counting
// attempts against an address would read a number that is too low — the failure
// direction that HIDES an attack.
func TestDigest_IsStableAndNormalisesTheAddressTheWayLoginDoes(t *testing.T) {
	d, err := NewDigest(testKey())
	require.NoError(t, err)

	canonical := d.Of("admin@acme.com")
	require.Len(t, canonical, 64)
	assert.Equal(t, canonical, d.Of("admin@acme.com"), "the digest must be stable")
	assert.Equal(t, canonical, d.Of("ADMIN@Acme.COM"), "case must not fork the subject")
	assert.Equal(t, canonical, d.Of("  admin@acme.com  "),
		"padding must not fork the subject: Login trims before it looks the address up")
	assert.NotEqual(t, canonical, d.Of("admin@acme.io"), "a different address is a different subject")
}

// The digest must be 64 lowercase hex characters — which is not cosmetic: the
// column is CHAR(64) with a hex CHECK, and that constraint is what makes "the
// submitted address is never stored in the clear" a property of the schema
// rather than a promise in a comment. Anything this produced that was not hex
// would be refused by the database.
func TestDigest_IsLowercaseHexSoTheColumnCheckCanEnforceIt(t *testing.T) {
	d, err := NewDigest(testKey())
	require.NoError(t, err)

	got := d.Of("someone@example.org")
	require.Len(t, got, 64)
	assert.Equal(t, strings.ToLower(got), got)
	assert.NotContains(t, got, "@")
	for _, c := range got {
		assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'), "non-hex byte %q", c)
	}
}

// KEYED, not hashed. Two deployments with different keys must not produce the
// same digest for the same address — otherwise a table of digests is a table of
// addresses to anyone who can compute an unkeyed hash of the world's e-mail
// addresses, and the pseudonym protects nobody.
func TestDigest_IsKeyed(t *testing.T) {
	a, err := NewDigest(testKey())
	require.NoError(t, err)
	other := testKey()
	other[0] ^= 0xff
	b, err := NewDigest(other)
	require.NoError(t, err)

	assert.NotEqual(t, a.Of("admin@acme.com"), b.Of("admin@acme.com"),
		"the digest must depend on the key, or it is a rainbow-table lookup away from the address")
}

// The key this uses is DERIVED from the master key, not the master key: holding
// the digest key must not confer the key that opens TOTP seeds and outbox
// payloads.
func TestDigest_KeyIsDerivedAndNotTheMasterKey(t *testing.T) {
	master := testKey()
	d, err := NewDigest(master)
	require.NoError(t, err)
	assert.NotEqual(t, master, d.key, "the digest key must be domain-separated from the master key")
}

// A build with no usable key still records events; it just cannot correlate
// unidentified ones. Losing the correlator is a documented degradation of a
// keyless development box, not a crash and not a silent success.
func TestDigest_WithoutAKeyIsANoOpRatherThanAFailure(t *testing.T) {
	_, err := NewDigest([]byte("too short"))
	require.Error(t, err)

	var absent *Digest
	assert.Empty(t, absent.Of("admin@acme.com"))
}

func TestDigest_EmptySubjectHasNoDigest(t *testing.T) {
	d, err := NewDigest(testKey())
	require.NoError(t, err)
	assert.Empty(t, d.Of(""))
	assert.Empty(t, d.Of("   "))
}

// --- the envelope ------------------------------------------------------------

// An event outside the vocabulary is a programming error and is refused: a row
// nobody can query for is worse than an error at the call site.
func TestEvent_ValidateRefusesWhatNobodyCouldQueryFor(t *testing.T) {
	base := Event{Event: EventLoginFailed, Outcome: OutcomeFailure, Method: MethodPassword, Reason: ReasonBadCredential}
	require.NoError(t, base.validate())

	for name, mutate := range map[string]func(Event) Event{
		"unknown event":   func(e Event) Event { e.Event = "auth.something.invented"; return e },
		"unknown outcome": func(e Event) Event { e.Outcome = "maybe"; return e },
		"unknown method":  func(e Event) Event { e.Method = "telepathy"; return e },
		"unknown reason":  func(e Event) Event { e.Reason = "because"; return e },
		// A failure with no class is the shape that makes the stream useless:
		// the response is one generic refusal for every case, so if the reason
		// is missing here it exists nowhere at all.
		"failure with no reason": func(e Event) Event { e.Reason = ""; return e },
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, mutate(base).validate())
		})
	}
}

func TestEvent_SuccessNeedsNoReason(t *testing.T) {
	e := Event{Event: EventLoginSucceeded, Outcome: OutcomeSuccess, Method: MethodPassword}
	assert.NoError(t, e.validate())
}

// A nil recorder is a valid no-op, so a use case can take one optionally.
func TestRecorder_NilIsANoOp(t *testing.T) {
	var absent *Recorder
	assert.NoError(t, absent.Record(context.Background(), Event{}))
	assert.NotPanics(t, func() { absent.Note(context.Background(), Event{}) })
}

// --- the client address ------------------------------------------------------

// What reaches the INET column. The port has to go (inet does not take one),
// an IPv4-mapped IPv6 address is unmapped so one client is one address however
// the socket presented it, and anything that is not an address at all is
// dropped rather than smuggled in — the column's type is half of the guarantee
// that no caller string can land in this table.
func TestClientAddr_NormalisesToWhatAnInetColumnCanHold(t *testing.T) {
	for name, tc := range map[string]struct{ raw, want string }{
		"ipv4 with a port":  {"203.0.113.7:54321", "203.0.113.7"},
		"bare ipv4":         {"203.0.113.7", "203.0.113.7"},
		"ipv6 with a port":  {"[2001:db8::1]:443", "2001:db8::1"},
		"bare ipv6":         {"2001:db8::1", "2001:db8::1"},
		"ipv4-mapped ipv6":  {"[::ffff:203.0.113.7]:443", "203.0.113.7"},
		"a hostname":        {"proxy.internal:8080", ""},
		"outright nonsense": {"not-an-address", ""},
		"nothing at all":    {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			got := clientAddrOf(WithClientAddr(context.Background(), tc.raw))
			if tc.want == "" {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tc.want, *got)
		})
	}
}

func TestClientAddr_AbsentFromAnUnboundContext(t *testing.T) {
	assert.Nil(t, clientAddrOf(context.Background()))
	assert.Empty(t, ClientAddr(context.Background()))
}

// --- the request id ----------------------------------------------------------

// The request id is CALLER-SUPPLIED whenever X-Request-Id is present, so it is
// the one value on the row that could carry arbitrary text into an append-only
// store. Only a UUID gets through; anything else correlates nothing rather than
// contaminating everything.
func TestRequestID_OnlyAUUIDReachesTheRow(t *testing.T) {
	const real = "0f8fad5b-d9cb-469f-a165-70867728950e"
	got := requestUUID(app.WithRequestId(context.Background(), real))
	require.NotNil(t, got)
	assert.Equal(t, real, *got)

	for _, bogus := range []string{
		"", "not-a-uuid", "jane.doe@example.com",
		"0f8fad5b-d9cb-469f-a165-70867728950", // one short
		"0f8fad5bd9cb469fa16570867728950e",    // unhyphenated
		"0f8fad5b-d9cb-469f-a165-7086772895ZZ",
	} {
		assert.Nil(t, requestUUID(app.WithRequestId(context.Background(), bogus)),
			"a request id of %q must not reach the row", bogus)
	}
}
