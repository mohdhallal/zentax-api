package dto

import (
	"time"

	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
)

const instantLayout = "2006-01-02T15:04:05.000Z"

func instant(t time.Time) string { return t.UTC().Format(instantLayout) }

// DocumentViewToJSON renders DocumentView (nullable strings → null).
func DocumentViewToJSON(v *domain.DocumentView) map[string]any {
	return map[string]any{
		"id":             v.ID,
		"workflowId":     v.WorkflowID,
		"taskInstanceId": v.TaskInstanceID,
		"category":       v.Category,
		"documentType":   v.DocumentType,
		"label":          v.Label,
		"notes":          v.Notes,
		"fileName":       v.FileName,
		"fileSize":       v.FileSize,
		"mimeType":       v.MimeType,
		"sha256":         v.SHA256,
		"version":        v.Version,
		"versionId":      v.VersionID,
		"uploadedBy":     v.UploadedBy,
		"uploadedByName": v.UploadedByName,
		"createdAt":      instant(v.CreatedAt),
		"updatedAt":      instant(v.UpdatedAt),
		"workflowName":   v.WorkflowName,
		"entityId":       v.EntityID,
		"entityName":     v.EntityName,
		"financialYear":  v.FinancialYear,
	}
}

// DocumentVersionToJSON renders DocumentVersionView.
func DocumentVersionToJSON(v *domain.DocumentVersion) map[string]any {
	return map[string]any{
		"id":             v.ID,
		"documentId":     v.DocumentID,
		"version":        v.Version,
		"fileName":       v.FileName,
		"fileSize":       v.FileSize,
		"mimeType":       v.MimeType,
		"sha256":         v.SHA256,
		"uploadedBy":     v.CreatedBy,
		"uploadedByName": v.UploadedByName,
		"createdAt":      instant(v.CreatedAt),
	}
}
