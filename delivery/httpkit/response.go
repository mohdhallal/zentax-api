package httpkit

import "github.com/mohamadhallal/zentax-api/delivery/httpkit/types"

func Ok(data any) *types.HttpResponse {
	return &types.HttpResponse{Status: 200, Data: data}
}

func Created(data any) *types.HttpResponse {
	return &types.HttpResponse{Status: 201, Data: data}
}

func Accepted(data any) *types.HttpResponse {
	return &types.HttpResponse{Status: 202, Data: data}
}

func NoContent() *types.HttpResponse {
	return &types.HttpResponse{Status: 204}
}
