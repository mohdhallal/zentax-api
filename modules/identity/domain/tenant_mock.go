package domain

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// TenantRepositoryMock is a testify mock of TenantRepository.
type TenantRepositoryMock struct {
	mock.Mock
}

var _ TenantRepository = (*TenantRepositoryMock)(nil)

func (m *TenantRepositoryMock) GetByID(ctx context.Context, id string) (*Tenant, error) {
	a := m.Called(ctx, id)
	return tenantOrNil(a.Get(0)), a.Error(1)
}

func (m *TenantRepositoryMock) Update(ctx context.Context, id, name, timezone string) (*Tenant, error) {
	a := m.Called(ctx, id, name, timezone)
	return tenantOrNil(a.Get(0)), a.Error(1)
}

func tenantOrNil(v any) *Tenant {
	if v == nil {
		return nil
	}
	return v.(*Tenant)
}

// AuditRecorderMock is a testify mock of AuditRecorder.
type AuditRecorderMock struct {
	mock.Mock
}

var _ AuditRecorder = (*AuditRecorderMock)(nil)

func (m *AuditRecorderMock) Record(ctx context.Context, action, resourceType, resourceID string, details map[string]any) error {
	return m.Called(ctx, action, resourceType, resourceID, details).Error(0)
}
