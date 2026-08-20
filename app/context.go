package app

import (
	"context"
)

type ctxKey string

const (
	ctxRequestId ctxKey = "requestId"
	ctxRequester ctxKey = "requester"
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
