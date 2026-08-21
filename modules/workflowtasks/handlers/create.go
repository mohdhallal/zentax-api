package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type CreateWorkflowTaskHandler struct {
	usecases domain.WorkflowTaskUseCases
}

func NewCreateWorkflowTaskHandler(uc domain.WorkflowTaskUseCases) *CreateWorkflowTaskHandler {
	return &CreateWorkflowTaskHandler{usecases: uc}
}

func (h *CreateWorkflowTaskHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodPost,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.WorkflowTaskWrite,
	}
}

func (h *CreateWorkflowTaskHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Body: dto.CreateWorkflowTaskBody{},
	}
}

func (h *CreateWorkflowTaskHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.CreateWorkflowTaskBody)

	wt, err := h.usecases.Create(r.Context(), domain.CreateWorkflowTaskInput{
		WorkflowID:             body.WorkflowID,
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

	return httpkit.Created(dto.WorkflowTaskToJSON(wt)), nil
}
