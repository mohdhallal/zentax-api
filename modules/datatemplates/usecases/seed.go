package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// SeedPredefined inserts the predefined templates the tenant does not have
// yet (matched by name within category=predefined) and returns the tenant's
// full predefined list. Idempotent: a second call inserts nothing and returns
// the same rows. Used by POST /data-templates/predefined and by
// cmd/seed-admin right after the tenant is created.
func (uc *UseCases) SeedPredefined(ctx context.Context) ([]domain.DataTemplate, error) {
	if err := uc.authorizer.EnsureEntity(ctx, "", authz.DataTemplateWrite); err != nil {
		return nil, err
	}

	// Names are unique per tenant ACROSS categories: a custom template that
	// took a predefined name is skipped rather than failing the whole seed.
	predefined := domain.CategoryPredefined
	existing, err := uc.repo.List(ctx, domain.ListDataTemplatesArgs{})
	if err != nil {
		return nil, err
	}
	have := make(map[string]bool, len(existing))
	for _, t := range existing {
		have[t.Name] = true
	}

	inserted := 0
	for _, tpl := range domain.PredefinedTemplates() {
		if have[tpl.Name] {
			continue
		}
		if _, err := uc.repo.Create(ctx, tpl); err != nil {
			return nil, err
		}
		inserted++
	}

	// One entry per seeding that actually inserted something; the resource is
	// the tenant's template registry, so the tenant id is the resource id
	// (audit_log.resource_id is a NOT NULL uuid).
	if inserted > 0 {
		if err := uc.audit.Record(ctx, "data_template.predefined_seeded", "data_template", app.GetTenantID(ctx),
			map[string]any{"inserted": inserted}); err != nil {
			return nil, err
		}
	}

	all, err := uc.repo.List(ctx, domain.ListDataTemplatesArgs{Category: &predefined})
	if err != nil {
		return nil, err
	}
	if all == nil {
		all = []domain.DataTemplate{}
	}
	return all, nil
}
