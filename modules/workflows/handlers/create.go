package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	"github.com/mohamadhallal/zentax-api/modules/workflows/dto"
)

type CreateWorkflowHandler struct {
	usecases domain.WorkflowUseCases
}

func NewCreateWorkflowHandler(uc domain.WorkflowUseCases) *CreateWorkflowHandler {
	return &CreateWorkflowHandler{usecases: uc}
}

func (h *CreateWorkflowHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodPost,
		Path:     "/",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *CreateWorkflowHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body: dto.CreateWorkflowBody{},
	}
}

func (h *CreateWorkflowHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.CreateWorkflowBody)

	wf, err := h.usecases.Create(r.Context(), domain.CreateWorkflowInput{
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
	})
	if err != nil {
		return nil, err
	}

	return httpkit.Created(dto.WorkflowToJSON(wf)), nil
}
