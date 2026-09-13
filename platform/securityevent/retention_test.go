package securityevent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A window under the floor must be refused BEFORE the database is asked. The
// db here is nil on purpose: if the check ever moved behind the query this test
// would panic rather than fail politely, which is the right noise for "the
// product asked Postgres to age out eleven months of authentication evidence
// and only Postgres said no".
func TestAWindowUnderTheFloorNeverReachesTheDatabase(t *testing.T) {
	_, err := NewRetention(nil, RetentionSettings{RetentionMonths: MinRetentionMonths - 1, MonthsAhead: 3}).
		Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "12-month floor")
}

// A run-ahead of zero would let a month arrive with nowhere to land, and a month
// that lands in security_events_default is a month retention can never reach —
// so it is refused for the same reason, and just as early.
func TestARunAheadOfNothingIsRefused(t *testing.T) {
	// 13 is the decided window (ADR-0007), written out rather than imported:
	// platform must not depend on config.
	_, err := NewRetention(nil, RetentionSettings{RetentionMonths: 13, MonthsAhead: 0}).
		Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nowhere to land")
}

func TestEmptyIsTheQuietOutcome(t *testing.T) {
	assert.True(t, RetentionResult{}.Empty())
	assert.False(t, RetentionResult{Dropped: []string{"security_events_y2025m07"}}.Empty())
	assert.False(t, RetentionResult{Created: []string{"security_events_y2027m01"}}.Empty())
	assert.False(t, RetentionResult{Deferred: []string{"security_events_y2027m01"}}.Empty())
	// A month that could not be given a partition is the one outcome an operator
	// has to act on, so it must never read as "nothing happened".
	assert.False(t, RetentionResult{Blocked: []string{"security_events_y2027m01"}}.Empty())
}
