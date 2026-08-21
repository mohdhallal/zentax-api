package dto

import "github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"

func WorkflowTaskToJSON(wt *domain.WorkflowTask) map[string]any {
	return map[string]any{
		"id":                     wt.ID,
		"workflowId":             wt.WorkflowID,
		"name":                   wt.Name,
		"description":            wt.Description,
		"taskType":               wt.TaskType,
		"roleLabel":              wt.RoleLabel,
		"approvalRequired":       wt.ApprovalRequired,
		"dueDateReference":       wt.DueDateReference,
		"dueDateOffsetValue":     wt.DueDateOffsetValue,
		"dueDateOffsetUnit":      wt.DueDateOffsetUnit,
		"dueDateOffsetDirection": wt.DueDateOffsetDirection,
		"orderIndex":             wt.OrderIndex,
		"dataTemplateId":         wt.DataTemplateID,
		"requiredDocuments":      wt.RequiredDocuments,
		"createdBy":              wt.CreatedBy,
		"updatedBy":              wt.UpdatedBy,
		"createdAt":              wt.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		"updatedAt":              wt.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}
