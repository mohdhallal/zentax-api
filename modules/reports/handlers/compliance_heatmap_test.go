package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/modules/reports/dto"
)

// heatmapStub answers ComplianceHeatmap with a fixed number of cells and
// records the arguments it was called with. The embedded nil Reader covers
// the rest of the interface (never called here).
type heatmapStub struct {
	domain.Reader
	cells int
	args  domain.HeatmapArgs
}

func (s *heatmapStub) ComplianceHeatmap(_ context.Context, args domain.HeatmapArgs) ([]domain.HeatmapCell, error) {
	s.args = args
	out := make([]domain.HeatmapCell, 0, s.cells)
	for i := 0; i < s.cells; i++ {
		out = append(out, domain.HeatmapCell{
			RowID: "e1", RowLabel: "Entity", ColID: fmt.Sprintf("2025:P%d", i), ColLabel: fmt.Sprintf("P%d (FY2025)", i),
			TotalTasks: 1,
		})
	}
	return out, nil
}

func heatmapRequest(t *testing.T, query dto.ComplianceHeatmapQuery) (*http.Request, *types.ValidatedInput) {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/reports/compliance-heatmap", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	return r, &types.ValidatedInput{Query: &query}
}

// The handler asks the repository for one cell past the cap and, when that
// extra row comes back, answers 400 VALIDATION with the "narrow it" message
// (ADR-0026 decision 7) — never a truncated grid.
func TestComplianceHeatmap_OverTheCapIs400(t *testing.T) {
	stub := &heatmapStub{cells: domain.MaxHeatmapCells + 1}
	h := NewComplianceHeatmapHandler(stub)
	r, input := heatmapRequest(t, dto.ComplianceHeatmapQuery{ViewMode: domain.ViewModePeriod})

	resp, err := h.Execute(nil, r, input, nil)
	if err == nil {
		t.Fatalf("expected an error, got %+v", resp)
	}
	if resp != nil {
		t.Fatalf("no response must accompany the error, got %+v", resp)
	}
	appErr := httperr.Classify(err)
	if appErr.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", appErr.Status)
	}
	if appErr.Code != httperr.ErrValidation {
		t.Fatalf("code = %s, want %s", appErr.Code, httperr.ErrValidation)
	}
	if appErr.Message != domain.ErrHeatmapTooLarge {
		t.Fatalf("message = %q, want %q", appErr.Message, domain.ErrHeatmapTooLarge)
	}
	if stub.args.Limit != domain.MaxHeatmapCells+1 {
		t.Fatalf("the repository must be asked for max+1 cells, got limit %d", stub.args.Limit)
	}
}

// Exactly the cap is a full grid, not an error; the filters and view mode
// reach the repository normalized ("all" = no year).
func TestComplianceHeatmap_AtTheCapIsAGrid(t *testing.T) {
	stub := &heatmapStub{cells: domain.MaxHeatmapCells}
	h := NewComplianceHeatmapHandler(stub)
	r, input := heatmapRequest(t, dto.ComplianceHeatmapQuery{
		ReportFilterQuery: dto.ReportFilterQuery{Year: str("all")},
		ViewMode:          domain.ViewModePeriod,
	})

	resp, err := h.Execute(nil, r, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d", resp.Status)
	}
	if stub.args.FinancialYear != nil {
		t.Fatalf("year=all must reach the repository as no filter, got %q", *stub.args.FinancialYear)
	}
	if stub.args.ViewMode != domain.ViewModePeriod {
		t.Fatalf("viewMode = %q", stub.args.ViewMode)
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Cols    []struct{ ID, Label string } `json:"cols"`
		Cells   []json.RawMessage            `json:"cells"`
		Summary struct{ TotalCells int }     `json:"summary"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Summary.TotalCells != domain.MaxHeatmapCells || len(body.Cells) != domain.MaxHeatmapCells {
		t.Fatalf("totalCells = %d, cells = %d, want %d", body.Summary.TotalCells, len(body.Cells), domain.MaxHeatmapCells)
	}
	if len(body.Cols) != domain.MaxHeatmapCells {
		t.Fatalf("cols = %d, want %d distinct qualified columns", len(body.Cols), domain.MaxHeatmapCells)
	}
}
