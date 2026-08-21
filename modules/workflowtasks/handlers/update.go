package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/dto"
)

type UpdateWorkflowTaskHandler struct {
	usecases domain.WorkflowTaskUseCases
}

func NewUpdateWorkflowTaskHandler(uc domain.WorkflowTaskUseCases) *UpdateWorkflowTaskHandler {
	return &UpdateWorkflowTaskHandler{usecases: uc}
}

func (h *UpdateWorkflowTaskHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodPut,
		Path:     "/{id}",
		Exposure: types.Exposures.External,
		Tenant:   true,
	}
}

func (h *UpdateWorkflowTaskHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body:   dto.UpdateWorkflowTaskBody{},
		Params: dto.WorkflowTaskIdParams{},
	}
}

func (h *UpdateWorkflowTaskHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.UpdateWorkflowTaskBody)
	params, _ := input.Params.(*dto.WorkflowTaskIdParams)

	wt, err := h.usecases.Update(r.Context(), params.ID, domain.UpdateWorkflowTaskInput{
		Name:                   body.Name,
		Description:            body.Description,
		TaskType:               body.TaskType,
		RoleLabel:              body.RoleLabel,
		ApprovalRequired:       body.ApprovalRequired,
		DueDateReference:       body.DueDateReference,
		DueDateOffsetValue:     body.DueDateOffsetValue,
		DueDateOffsetUnit:      body.DueDateOffsetUnit,
		DueDateOffsetDirection: body.DueDateOffsetDirection,
		OrderIndex:             body.OrderIndex,
		DataTemplateID:         body.DataTemplateID,
		RequiredDocuments:      body.RequiredDocuments,
	})
	if err != nil {
		return nil, err
	}

	return httpkit.Ok(dto.WorkflowTaskToJSON(wt)), nil
}
