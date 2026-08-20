package middlewares

import (
	"net/http"
	"strconv"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/auth/domain"
)

// RequireExternalAuth trusts headers injected by the upstream auth gateway.
// No credential lookup — the gateway has already validated the caller.
func RequireExternalAuth() types.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			accountID, err := strconv.Atoi(r.Header.Get("X-Account-Id"))
			if err != nil || accountID == 0 {
				httperr.HandleError(w, r, httperr.New(httperr.ErrUnauthorized, "Authentication required"))
				return
			}

			apiKey, apiSecret, _ := r.BasicAuth()

			requester := &app.Requester{
				Kind:          app.RequesterUser,
				ID:            strconv.Itoa(accountID),
				AccountID:     accountID,
				APIKeyID:      headerInt(r, "X-API-Key-Id"),
				CustomerID:    headerInt(r, "X-Customer-Id"),
				CorrelationID: r.Header.Get("X-Correlation-Id"),
				ClientIP:      r.Header.Get("X-Client-IP"),
				APIKey:        apiKey,
				APISecret:     apiSecret,
			}

			ctx := app.WithRequester(r.Context(), requester)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireInternalAuth validates Basic Auth credentials against internal_api_keys.
func RequireInternalAuth(validator domain.Validator) types.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, secret, ok := r.BasicAuth()
			if !ok || key == "" {
				httperr.HandleError(w, r, httperr.New(httperr.ErrUnauthorized, "Authentication required"))
				return
			}

			result, err := validator.Validate(r.Context(), key, secret)
			if err != nil {
				httperr.HandleError(w, r, httperr.New(httperr.ErrUnauthorized, err.Error()))
				return
			}

			requester := &app.Requester{
				Kind:        app.RequesterService,
				ID:          result.ServiceName,
				ServiceName: result.ServiceName,
			}

			ctx := app.WithRequester(r.Context(), requester)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func headerInt(r *http.Request, key string) int {
	n, _ := strconv.Atoi(r.Header.Get(key))
	return n
}
