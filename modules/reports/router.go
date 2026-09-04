// Package reports exposes read-only, cross-entity views under /reports — the
// aggregates the frontend dashboards and task lists need in one round trip.
// No writes, no use cases: handlers read straight through the domain.Reader.
package reports

import (
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/modules/reports/handlers"
)

func RegisterRoutes(router *routing.Router, reader domain.Reader) {
	router.Group("/reports", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewTaskInstancesReportHandler(reader))
		routing.RegisterRoute(r, handlers.NewWorkflowStatsHandler(reader))
		// Compliance / financial reports (ADR-0021): SQL aggregations, capped
		// row lists, legacy parameter names + response shapes.
		routing.RegisterRoute(r, handlers.NewComplianceHeatmapHandler(reader))
		routing.RegisterRoute(r, handlers.NewComplianceStatusHandler(reader))
		routing.RegisterRoute(r, handlers.NewTaxFinancialHandler(reader))
		routing.RegisterRoute(r, handlers.NewExportRawHandler(reader))
	})
}
