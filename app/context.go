package app

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

type ctxKey string

const (
	ctxRequestId ctxKey = "requestId"
	ctxRequester ctxKey = "requester"
	ctxTenantId  ctxKey = "tenantId"
)

// RequestIDLength is the length of the one shape a request id may have: a
// canonical 8-4-4-4-12 hex UUID.
const RequestIDLength = 36

// NewRequestID mints a request id in the canonical shape.
func NewRequestID() string { return uuid.NewString() }

// NormalizeRequestID is the gate on the only value in the request envelope a
// CALLER supplies: the X-Request-Id header. It returns the canonical form —
// lowercase 8-4-4-4-12 hex — and false for anything else.
//
// Why a shape at all: this id is written verbatim into audit_log, folded into
// the per-tenant hash chain, and copied into WORM segments that a
// compliance-locked bucket will hold for a decade and no principal can delete
// from. Unbounded caller text in that store contradicts the ADR-0008 rule that
// the envelope carries no free text, and there is no way to take it back. The
// sibling stream already learned this — security_events.request_id is a UUID
// column with a validator in front of it — and this is the same door on the
// store with the worse deletion story.
//
// Case is normalized rather than refused so that the two streams correlate: the
// security stream's column is UUID, which Postgres renders lowercase, and a
// request id spelled in capitals would otherwise join to nothing.
func NormalizeRequestID(s string) (string, bool) {
	if len(s) != RequestIDLength {
		return "", false
	}
	for i := range RequestIDLength {
		c := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return "", false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return "", false
		}
	}
	return strings.ToLower(s), true
}

// WithRequestId binds the request's correlation id. Callers outside a test bind
// what RequestIdMiddleware resolved; every store that persists the value gates
// it through NormalizeRequestID, so an id of any other shape correlates nothing
// rather than reaching an append-only table.
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
