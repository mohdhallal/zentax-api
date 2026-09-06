package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// The completion instant is where a civil date meets a timestamp, and getting
// it wrong is invisible: the row still says "completed", the report still
// returns a number, and only the classification (on time vs late) or the
// displayed date quietly moves by a day. The compliance SQL reads
// (completed_at AT TIME ZONE tenant.timezone)::date, so an instant computed in
// UTC instead of the tenant's zone lands on the wrong civil day for exactly the
// completions the demo cares about — an evening filing in New York, a morning
// one in Tokyo.
//
// These tests pin the rule in the three demo zones, on both sides of both
// daylight-saving transitions of 2026.

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	zone, err := time.LoadLocation(name)
	require.NoError(t, err)
	return zone
}

func TestInstantsForAcrossTheDemoZones(t *testing.T) {
	tests := []struct {
		name      string
		zone      string
		date      dateonly.Date
		localTime string
		want      string
	}{
		{
			name: "Berlin winter is UTC+1",
			zone: "Europe/Berlin", date: dateonly.New(2026, 2, 9), want: "2026-02-09T09:00:00Z",
		},
		{
			// The dataset's own globex example: an evening filing in New York
			// falls on the NEXT UTC day.
			name: "New York evening crosses the UTC date line",
			zone: "America/New_York", date: dateonly.New(2026, 4, 15), localTime: "23:30",
			want: "2026-04-16T03:30:00Z",
		},
		{
			// And the initech one: a Tokyo morning falls on the PREVIOUS UTC day.
			name: "Tokyo morning falls on the previous UTC day",
			zone: "Asia/Tokyo", date: dateonly.New(2026, 5, 29), localTime: "07:30",
			want: "2026-05-28T22:30:00Z",
		},
		{
			name: "Berlin the day before the spring transition is still UTC+1",
			zone: "Europe/Berlin", date: dateonly.New(2026, 3, 28), want: "2026-03-28T09:00:00Z",
		},
		{
			// 2026-03-29 is the EU spring-forward Sunday: 10:00 local is CEST.
			name: "Berlin on the spring-forward Sunday is UTC+2",
			zone: "Europe/Berlin", date: dateonly.New(2026, 3, 29), want: "2026-03-29T08:00:00Z",
		},
		{
			name: "New York the day before the autumn transition is UTC-4",
			zone: "America/New_York", date: dateonly.New(2026, 10, 31), want: "2026-10-31T14:00:00Z",
		},
		{
			// 2026-11-01 is the US fall-back Sunday: 10:00 local is EST.
			name: "New York on the fall-back Sunday is UTC-5",
			zone: "America/New_York", date: dateonly.New(2026, 11, 1), want: "2026-11-01T15:00:00Z",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			instance := spec.Instance{
				Period: "M1", Task: "file", Status: spec.InstanceCompleted, Via: spec.ViaPut,
				CompletedOn: tc.date, CompletedAtLocalTime: tc.localTime,
			}
			row, ok, err := instantsFor(instance, mustZone(t, tc.zone), spec.DefaultCompletionLocalTime)
			require.NoError(t, err)
			require.True(t, ok)
			require.NotNil(t, row.CompletedAt)
			assert.Equal(t, tc.want, row.CompletedAt.UTC().Format(time.RFC3339))
			assert.Equal(t, time.UTC, row.CompletedAt.Location())
			// A via=put completion never went through an approval.
			assert.Nil(t, row.SubmittedAt)
			assert.Nil(t, row.ApprovedAt)
			// updated_at is the completion: it is the row's last real change.
			assert.Equal(t, *row.CompletedAt, row.updatedAt())
		})
	}
}

func TestInstantsForApprovedInstance(t *testing.T) {
	instance := spec.Instance{
		Period: "M1", Task: "review", Status: spec.InstanceCompleted, Via: spec.ViaApprove,
		SubmittedOn: dateonly.New(2026, 2, 11),
		CompletedOn: dateonly.New(2026, 2, 12),
	}
	row, ok, err := instantsFor(instance, mustZone(t, "Europe/Berlin"), spec.DefaultCompletionLocalTime)
	require.NoError(t, err)
	require.True(t, ok)

	require.NotNil(t, row.SubmittedAt)
	assert.Equal(t, "2026-02-11T09:00:00Z", row.SubmittedAt.UTC().Format(time.RFC3339))
	require.NotNil(t, row.CompletedAt)
	assert.Equal(t, "2026-02-12T09:00:00Z", row.CompletedAt.UTC().Format(time.RFC3339))
	// approved_at equals completed_at: the approval IS the completion.
	require.NotNil(t, row.ApprovedAt)
	assert.Equal(t, *row.CompletedAt, *row.ApprovedAt)
}

func TestInstantsForUsesTheDatasetDefaultTime(t *testing.T) {
	instance := spec.Instance{
		Period: "Y1", Task: "file", Status: spec.InstanceCompleted, Via: spec.ViaPut,
		CompletedOn: dateonly.New(2026, 6, 1),
	}
	row, _, err := instantsFor(instance, mustZone(t, "Europe/Berlin"), "16:45")
	require.NoError(t, err)
	require.NotNil(t, row.CompletedAt)
	assert.Equal(t, "2026-06-01T14:45:00Z", row.CompletedAt.UTC().Format(time.RFC3339))
}

func TestInstantsForNothingToBackdate(t *testing.T) {
	instance := spec.Instance{Period: "M8", Task: "review", Status: spec.InstancePendingApproval, Via: spec.ViaSubmit}
	row, ok, err := instantsFor(instance, mustZone(t, "Asia/Tokyo"), spec.DefaultCompletionLocalTime)
	require.NoError(t, err)
	assert.False(t, ok, "a pending_approval instance keeps the API's own stamps")
	assert.Zero(t, row)
}

func TestInstantsForRejectsAContradictoryCompletedAtUtc(t *testing.T) {
	instance := spec.Instance{
		Period: "Y1", Task: "file", Status: spec.InstanceCompleted, Via: spec.ViaPut,
		CompletedOn: dateonly.New(2026, 4, 15), CompletedAtLocalTime: "23:30",
		// Wrong on purpose: this is the civil date stamped as if it were UTC,
		// the exact mistake the field exists to catch.
		CompletedAtUtc: time.Date(2026, 4, 15, 23, 30, 0, 0, time.UTC),
	}
	_, _, err := instantsFor(instance, mustZone(t, "America/New_York"), spec.DefaultCompletionLocalTime)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2026-04-15T23:30:00Z")
	assert.Contains(t, err.Error(), "2026-04-16T03:30:00Z")
}

func TestInstantsForAcceptsAMatchingCompletedAtUtc(t *testing.T) {
	instance := spec.Instance{
		Period: "Y1", Task: "pay", Status: spec.InstanceCompleted, Via: spec.ViaPut,
		CompletedOn: dateonly.New(2026, 5, 29), CompletedAtLocalTime: "07:30",
		CompletedAtUtc: time.Date(2026, 5, 28, 22, 30, 0, 0, time.UTC),
	}
	row, ok, err := instantsFor(instance, mustZone(t, "Asia/Tokyo"), spec.DefaultCompletionLocalTime)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, instance.CompletedAtUtc, row.CompletedAt.UTC())
}

func TestInstantsForNeedsAZone(t *testing.T) {
	instance := spec.Instance{CompletedOn: dateonly.New(2026, 1, 1)}
	_, _, err := instantsFor(instance, nil, spec.DefaultCompletionLocalTime)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timezone")
}
