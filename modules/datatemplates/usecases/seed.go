package usecases

import (
	"context"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
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

	// Each insert is recorded as the schema it wrote, through the same
	// data_template.created action and the same whitelist the custom route
	// uses. A predefined template is not a lesser object: its numericValidation
	// decides which figures the system will accept from every task instance
	// that attaches it, and until now three of them entered a tenant behind a
	// single {"inserted": 3} — the one shape the third pass over this envelope
	// exists to remove, since a count made "the VAT schema arrived" identical
	// to "some templates arrived". Recording each one also puts a real
	// data_template id in resource_id, so the resource index finds a
	// predefined template's history the way it finds a custom one's.
	inserted := 0
	for _, tpl := range domain.PredefinedTemplates() {
		if have[tpl.Name] {
			continue
		}
		created, err := uc.repo.Create(ctx, tpl)
		if err != nil {
			return nil, err
		}
		if err := uc.audit.Record(ctx, "data_template.created", "data_template", created.ID,
			audit.Changes(nil, auditValues(created))); err != nil {
			return nil, err
		}
		inserted++
	}

	// Plus one entry for the seeding ACT, which the per-template entries cannot
	// express: that these templates arrived together, in one call, and how many
	// of the catalogue the tenant was missing. Its resource is the tenant — the
	// registry that was seeded — and it now SAYS so. It used to declare
	// resource_type "data_template" while passing the tenant id as resource_id,
	// the only call site in the tree whose resource_id was not a row of its
	// declared type, which put a non-template id into the resource index and
	// made the entry unjoinable to anything.
	if inserted > 0 {
		if err := uc.audit.Record(ctx, "data_template.predefined_seeded", "tenant", app.GetTenantID(ctx),
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
