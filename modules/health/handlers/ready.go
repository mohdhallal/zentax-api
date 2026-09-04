package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/logger"
)

// ReadinessTimeout bounds the database ping: a hung pool must turn into a
// 503, not a probe that never answers.
const ReadinessTimeout = 2 * time.Second

// Pinger answers whether the database is reachable (`SELECT 1`). Implemented
// by database.Pinger over the API's connection pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

// ReadyResponse is the body of a passing readiness check.
type ReadyResponse struct {
	DB string `json:"db"`
}

// ReadyHandler serves GET /health/ready — the READINESS probe. Unlike /health
// (liveness: the process is up, no dependencies) it fails when the database
// does not answer within ReadinessTimeout, so the load balancer stops routing
// to this task while it cannot serve requests.
type ReadyHandler struct {
	db      Pinger
	timeout time.Duration
}

func NewReadyHandler(db Pinger) *ReadyHandler {
	return &ReadyHandler{db: db, timeout: ReadinessTimeout}
}

func (h *ReadyHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodGet,
		Path:     "/ready",
		Exposure: types.Exposures.Both,
	}
}

func (h *ReadyHandler) Execute(w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester) (*types.HttpResponse, error) {
	if h.db == nil {
		return nil, httperr.New(httperr.ErrUnavailable, "database not configured")
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	if err := h.db.Ping(ctx); err != nil {
		// The cause goes to the log (no connection string, no credentials —
		// pgx errors carry neither), not to the probe's caller.
		logger.Log.WithContext(r.Context()).Warn("readiness: database ping failed", logger.Error(err))
		return nil, httperr.New(httperr.ErrUnavailable, "database unavailable")
	}
	return httpkit.Ok(&ReadyResponse{DB: "ok"}), nil
}
