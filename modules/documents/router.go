// Package documents exposes the document API (ADR-0022): uploads and lists
// scoped to a workflow / task instance, and the /documents resource (repository
// view, versions, downloads, metadata, soft delete).
package documents

import (
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
	"github.com/mohamadhallal/zentax-api/modules/documents/handlers"
)

// RegisterRoutes mounts the workflow- / task-instance-scoped routes on the root
// router (their full paths sit beside the /workflows and /task-instances
// groups) and the /documents group. maxUploadBytes is the per-file cap.
func RegisterRoutes(router *routing.Router, uc domain.DocumentUseCases, maxUploadBytes int64) {
	routing.RegisterRoute(router, handlers.NewUploadDocumentHandler(uc, maxUploadBytes))
	routing.RegisterRoute(router, handlers.NewListByWorkflowHandler(uc))
	routing.RegisterRoute(router, handlers.NewListByTaskInstanceHandler(uc))

	router.Group("/documents", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewListDocumentsHandler(uc))
		routing.RegisterRoute(r, handlers.NewGetDocumentHandler(uc))
		routing.RegisterRoute(r, handlers.NewUpdateDocumentHandler(uc))
		routing.RegisterRoute(r, handlers.NewDeleteDocumentHandler(uc))
		routing.RegisterRoute(r, handlers.NewListVersionsHandler(uc))
		routing.RegisterRoute(r, handlers.NewAddVersionHandler(uc, maxUploadBytes))
		routing.RegisterRoute(r, handlers.NewDownloadDocumentHandler(uc))
		routing.RegisterRoute(r, handlers.NewDownloadVersionHandler(uc))
	})
}
