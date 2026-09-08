package dto

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// completionRate rounds half-up like WorkflowStats.completionPercent and is 0
// on an empty set — never a division by zero, never a float.
func TestTaskSummaryToJSON_CompletionRateRounding(t *testing.T) {
	cases := []struct {
		completed, total, want int
	}{
		{0, 0, 0},
		{1, 3, 33},
		{2, 3, 67},
		{1, 8, 13}, // 12.5 rounds up
		{1, 2, 50},
		{5, 5, 100},
		{0, 7, 0},
	}
	for _, tc := range cases {
		out := TaskSummaryToJSON(&domain.TaskSummary{Completed: tc.completed, Total: tc.total})
		if got := out["completionRate"]; got != tc.want {
			t.Fatalf("%d/%d: completionRate = %v, want %d", tc.completed, tc.total, got, tc.want)
		}
	}
	// One rounding rule for both aggregates.
	if (domain.WorkflowStats{CompletedTasks: 1, TotalTasks: 3}).CompletionPercent() !=
		(domain.TaskSummary{Completed: 1, Total: 3}).CompletionRate() {
		t.Fatal("the summary and workflow-stats must round the same way")
	}
}

// Every key is always present, byStatus carries all six statuses (0 when
// none), and today is the civil date (null only when unknown).
func TestTaskSummaryToJSON_EveryKeyAlwaysPresent(t *testing.T) {
	out := TaskSummaryToJSON(&domain.TaskSummary{
		Today: dateonly.New(2026, 9, 8), Total: 243, Completed: 120, Active: 123, Overdue: 12,
		DueToday: 3, DueThisWeek: 9, AwaitingApproval: 4,
		NotStarted: 60, InProgress: 40, InReview: 10, PendingApproval: 4, Blocked: 9,
	})
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(decoded))
	for k := range decoded {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{
		"active", "awaitingApproval", "byStatus", "completed", "completionRate",
		"dueThisWeek", "dueToday", "overdue", "today", "total",
	}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys = %v, want %v", keys, want)
		}
	}
	if decoded["today"] != "2026-09-08" {
		t.Fatalf("today = %v", decoded["today"])
	}
	if decoded["completionRate"] != float64(49) {
		t.Fatalf("completionRate = %v", decoded["completionRate"])
	}
	byStatus, _ := decoded["byStatus"].(map[string]any)
	wantStatus := map[string]float64{
		"not_started": 60, "in_progress": 40, "in_review": 10, "pending_approval": 4, "completed": 120, "blocked": 9,
	}
	if len(byStatus) != len(wantStatus) {
		t.Fatalf("byStatus = %v", byStatus)
	}
	for k, v := range wantStatus {
		if byStatus[k] != v {
			t.Fatalf("byStatus[%s] = %v, want %v", k, byStatus[k], v)
		}
	}

	// An empty set still carries every key, the six statuses at 0, and a
	// missing today renders as null rather than a zero date.
	empty := TaskSummaryToJSON(&domain.TaskSummary{})
	if empty["today"] != nil {
		t.Fatalf("today must be null when unknown, got %v", empty["today"])
	}
	zero, _ := empty["byStatus"].(map[string]int)
	if len(zero) != 6 {
		t.Fatalf("byStatus must carry six keys, got %v", zero)
	}
	for _, status := range domain.TaskStatuses {
		if n, ok := zero[status]; !ok || n != 0 {
			t.Fatalf("byStatus[%s] must be present at 0, got %v", status, zero)
		}
	}
}
