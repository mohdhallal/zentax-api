// Package imports is spreadsheet ingest (PB-C5): the path by which a customer's
// existing tax book enters the product, as upload → dry run → commit.
package imports

import (
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
	"github.com/mohamadhallal/zentax-api/modules/imports/handlers"
)

// RegisterRoutes mounts the /imports group.
//
// The routes are registered once PER KIND rather than once with the kind in the
// path, so that each one can declare the capability it needs in its own route
// definition: /imports/entities requires entity:write, /imports/entity-
// obligations requires entity_obligation:write, and both are enforced by the
// framework before the handler runs. A single kind-in-the-path route could not
// declare either, and the tenant-wide gate would have to move out of the route
// table and into the use case, where nobody reading the API's surface would
// find it.
//
// Order matters within a kind: /{kind}/template and /{kind}/template.csv are
// registered before /{kind}/{id} so a literal segment is never shadowed by the
// parameter.
//
// The two template routes are one column set in two forms: `template` DESCRIBES
// the columns (JSON, for a screen that renders them as a table) and
// `template.csv` IS the blank sheet (an attachment, for a customer who wants
// something to type into). They are separate paths rather than one
// content-negotiated route because a download link cannot set an Accept header.
// `template` carries the configured bounds as well as the columns, which is why
// it takes the same `bounds` the upload route enforces: the screen that tells a
// customer what will fit must be told by the server that decides it.
func RegisterRoutes(router *routing.Router, uc domain.ImportUseCases, bounds handlers.Bounds) {
	router.Group("/imports", func(r *routing.Router) {
		for _, kind := range []domain.Kind{domain.KindEntities, domain.KindEntityObligations} {
			routing.RegisterRoute(r, handlers.NewUploadHandler(uc, kind, bounds))
			routing.RegisterRoute(r, handlers.NewListBatchesHandler(uc, kind))
			routing.RegisterRoute(r, handlers.NewTemplateHandler(uc, kind, bounds))
			routing.RegisterRoute(r, handlers.NewTemplateSheetHandler(uc, kind))
			routing.RegisterRoute(r, handlers.NewGetBatchHandler(uc, kind))
			routing.RegisterRoute(r, handlers.NewListRowsHandler(uc, kind))
			routing.RegisterRoute(r, handlers.NewCommitHandler(uc, kind))
		}
	})
}
