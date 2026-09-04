// Package auditlog exposes the read API for the application audit trail
// (ADR-0008): GET /audit-log. Listing only — there is no write surface; the
// trail is appended by platform/audit on every domain mutation.
package auditlog

import (
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/modules/auditlog/domain"
	"github.com/mohamadhallal/zentax-api/modules/auditlog/handlers"
)

func RegisterRoutes(router *routing.Router, reader domain.Reader) {
	router.Group("/audit-log", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewListAuditLogHandler(reader))
	})
}
