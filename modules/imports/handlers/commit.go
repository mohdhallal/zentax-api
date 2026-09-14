package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
	"github.com/mohamadhallal/zentax-api/modules/imports/dto"
)

// CommitHandler: POST /imports/{kind}/{id}/commit — the deliberate act.
//
// A separate request from the upload, and a POST with no body, because this is
// the moment the customer's tax book changes and it must be something they did
// rather than something that happened while they were looking at a preview.
//
// Tenant: true gives it the request transaction, and that transaction is the
// all-or-nothing guarantee: the records, the audit entries that describe them
// and the batch's own change of state commit together or not at all.
type CommitHandler struct {
	usecases domain.ImportUseCases
	kind     domain.Kind
}

func NewCommitHandler(uc domain.ImportUseCases, kind domain.Kind) *CommitHandler {
	return &CommitHandler{usecases: uc, kind: kind}
}

func (h *CommitHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/" + string(h.kind.Target()) + "/{id}/commit",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: writeCapability(h.kind),
	}
}

func (h *CommitHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Params: dto.BatchIdParams{}}
}

func (h *CommitHandler) Execute(
	_ http.ResponseWriter, r *http.Request, input *types.ValidatedInput, _ *app.Requester,
) (*types.HttpResponse, error) {
	params, _ := input.Params.(*dto.BatchIdParams)

	batch, err := h.usecases.Commit(r.Context(), h.kind, params.ID)
	if err != nil {
		return nil, err
	}
	return httpkit.Ok(dto.BatchToJSON(batch)), nil
}
