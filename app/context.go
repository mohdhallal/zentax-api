package app

import (
	"context"
)

type ctxKey string

const (
	ctxRequestId ctxKey = "requestId"
	ctxRequester ctxKey = "requester"
	ctxTenantId  ctxKey = "tenantId"
)

func WithRequestId(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxRequestId, id)
}

func GetRequestId(ctx context.Context) string {
	if v, ok := ctx.Value(ctxRequestId).(string); ok {
		return v
	}
	return ""
}

func WithRequester(ctx context.Context, r *Requester) context.Context {
	return context.WithValue(ctx, ctxRequester, r)
}

func GetRequester(ctx context.Context) *Requester {
	if v, ok := ctx.Value(ctxRequester).(*Requester); ok {
		return v
	}
	return nil
}

// WithTenantID binds the caller's tenant to the context. The Tx seam
// (database.Exec.WithinTransaction) reads it and sets the `app.tenant_id`
// Postgres GUC so RLS isolates every query to that tenant (ADR-0004).
func WithTenantID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxTenantId, id)
}

// GetTenantID returns the tenant bound to the context, or "" if none.
func GetTenantID(ctx context.Context) string {
	if v, ok := ctx.Value(ctxTenantId).(string); ok {
		return v
	}
	return ""
}
