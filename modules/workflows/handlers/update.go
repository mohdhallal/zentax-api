package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	"github.com/mohamadhallal/zentax-api/modules/workflows/dto"
)

type UpdateWorkflowHandler struct {
	usecases domain.WorkflowUseCases
}

func NewUpdateWorkflowHandler(uc domain.WorkflowUseCases) *UpdateWorkflowHandler {
	return &UpdateWorkflowHandler{usecases: uc}
}

func (h *UpdateWorkflowHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodPut,
		Path:     "/{id}",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *UpdateWorkflowHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body:   dto.UpdateWorkflowBody{},
		Params: dto.WorkflowIdParams{},
	}
}

func (h *UpdateWorkflowHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.UpdateWorkflowBody)
	params, _ := input.Params.(*dto.WorkflowIdParams)

	wf, err := h.usecases.Update(r.Context(), params.ID, domain.UpdateWorkflowInput{
		Name:             body.Name,
		Description:      body.Description,
		WorkflowCategory: body.WorkflowCategory,
		ProjectType:      body.ProjectType,
		FinancialYear:    body.FinancialYear,
		Periodicity:      body.Periodicity,
		SelectedPeriods:  body.SelectedPeriods,
		EntityID:         body.EntityID,
		ObligationTypeID: body.ObligationTypeID,
		DueDateRule:      body.DueDateRule,
		StartDate:        body.StartDate,
		EndDate:          body.EndDate,
		TasksSequential:  body.TasksSequential,
		Status:           body.Status,
	})
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.WorkflowToJSON(wf)), nil
}
