package pg

import (
	"context"

	"github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
	taskinstancesdomain "github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// TemplateResolver implements the task-instances TemplateResolver port: it
// loads the template a task instance's tax data is validated against. The
// tenant comes from the request context (RLS) — another tenant's template id
// resolves to nil, exactly like a missing one.
type TemplateResolver struct {
	repo *DataTemplateRepo
}

var _ taskinstancesdomain.TemplateResolver = (*TemplateResolver)(nil)

func NewTemplateResolver(db database.ExecerPg) *TemplateResolver {
	return &TemplateResolver{repo: NewDataTemplateRepo(db)}
}

func (r *TemplateResolver) ResolveTemplate(ctx context.Context, templateID string) (*domain.DataTemplate, error) {
	if templateID == "" {
		return nil, nil //nolint:nilnil // nil,nil means not found
	}
	return r.repo.GetById(ctx, templateID)
}
