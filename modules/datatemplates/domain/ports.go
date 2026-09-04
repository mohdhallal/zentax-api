package domain

import "context"

type DataTemplateRepository interface {
	Create(ctx context.Context, input CreateDataTemplateInput) (*DataTemplate, error)
	GetById(ctx context.Context, id DataTemplateID) (*DataTemplate, error)
	Update(ctx context.Context, id DataTemplateID, input UpdateDataTemplateInput) (*DataTemplate, error)
	Delete(ctx context.Context, id DataTemplateID) (bool, error)
	// List returns the tenant's templates matching the filters, sorted by name.
	List(ctx context.Context, args ListDataTemplatesArgs) ([]DataTemplate, error)
	// ReferenceCount reports how many workflow tasks + task instances of the
	// tenant point at the template — a referenced template cannot be deleted.
	ReferenceCount(ctx context.Context, id DataTemplateID) (int, error)
}

type DataTemplateUseCases interface {
	Create(ctx context.Context, input CreateDataTemplateInput) (*DataTemplate, error)
	GetById(ctx context.Context, id DataTemplateID) (*DataTemplate, error)
	Update(ctx context.Context, id DataTemplateID, input UpdateDataTemplateInput) (*DataTemplate, error)
	Delete(ctx context.Context, id DataTemplateID) error
	List(ctx context.Context, args ListDataTemplatesArgs) ([]DataTemplate, error)
	// SeedPredefined idempotently inserts the predefined templates the tenant
	// is missing (matched by name + category) and returns the full predefined
	// list.
	SeedPredefined(ctx context.Context) ([]DataTemplate, error)
}
