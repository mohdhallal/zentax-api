package dto

import (
	"testing"
	"time"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// With no year selected the repository keys period columns by financial year
// (ADR-0026 decision 7); the renderer must keep every such column distinct
// and order them by the calendar (first period end), then label — so M1 of
// FY2025 precedes M1 of FY2026, and within a year M2 precedes M10.
func TestHeatmapToJSON_QualifiedColumnsStayDistinctAndFollowTheCalendar(t *testing.T) {
	col := func(fy, code string, end time.Time) domain.HeatmapCell {
		return domain.HeatmapCell{
			RowID: "e1", RowLabel: "Acme", TotalTasks: 1,
			ColID: domain.HeatmapColumnID(fy, code), ColLabel: domain.HeatmapColumnLabel(fy, code),
			FirstPeriodEnd: end,
		}
	}
	// Arrival order is the SQL's (row label, first period end, col id).
	out := HeatmapToJSON([]domain.HeatmapCell{
		col("2025", "M1", day(2025, 1, 31)),
		col("2025", "M2", day(2025, 2, 28)),
		col("2025", "M10", day(2025, 10, 31)),
		col("2026", "M1", day(2026, 1, 31)),
		col("2026", "M2", day(2026, 2, 28)),
	})
	cols, _ := out["cols"].([]idLabel)
	want := []idLabel{
		{"2025:M1", "M1 (FY2025)"}, {"2025:M2", "M2 (FY2025)"}, {"2025:M10", "M10 (FY2025)"},
		{"2026:M1", "M1 (FY2026)"}, {"2026:M2", "M2 (FY2026)"},
	}
	if len(cols) != len(want) {
		t.Fatalf("cols = %v, want %v", cols, want)
	}
	for i := range want {
		if cols[i] != want[i] {
			t.Fatalf("cols[%d] = %v, want %v (all: %v)", i, cols[i], want[i], cols)
		}
	}
	summary, _ := out["summary"].(map[string]int)
	if summary["totalCells"] != 5 {
		t.Fatalf("totalCells = %d, want 5 (one per year × period)", summary["totalCells"])
	}
	if rows, _ := out["rows"].([]idLabel); len(rows) != 1 {
		t.Fatalf("rows = %v", rows)
	}
}

// The column identity helpers are the contract the SQL builds by hand.
func TestHeatmapColumnIdentity(t *testing.T) {
	if got := domain.HeatmapColumnID("2019", "M1"); got != "2019:M1" {
		t.Fatalf("id = %q", got)
	}
	if got := domain.HeatmapColumnLabel("2019", "M1"); got != "M1 (FY2019)" {
		t.Fatalf("label = %q", got)
	}
}
