package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/modules/identity/dto"
)

type LoginHandler struct {
	usecases domain.AuthUseCases
	cookie   CookieConfig
}

func NewLoginHandler(uc domain.AuthUseCases, cookie CookieConfig) *LoginHandler {
	return &LoginHandler{usecases: uc, cookie: cookie}
}

// DefineRoute deliberately declares NO transaction. The use case owns its
// transactions instead, and takes them sequentially, because login must do two
// things a single request-long transaction cannot do together:
//
//   - charge the attempt budget in a statement that COMMITS BEFORE the password
//     is verified (a request-long transaction would hold the row lock across
//     ~70ms of argon2, serialising the whole account behind each guess, and
//     would roll the charge back with the 401 the same request answers — the
//     original defect, where six wrong passwords left the counter at 0);
//   - never hold two pooled connections at once, which rules out writing the
//     charge on a second connection borrowed while the request holds the first
//     (measured at PoolMax=5: a burst of 5 took 5.1s and recorded 1 of 5).
//
// The route needs nothing else the transaction middleware provides: it is
// public, so there is no tenant or user GUC to bind (ADR-0004/0008 — the seam
// binds them only when the context carries a tenant, which a pre-auth request
// never does), and login writes no audit entry. CSRF, which does apply, is
// wired in the route builder around every external route, not here.
func (h *LoginHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:   http.MethodPost,
		Path:     "/login",
		Exposure: types.Exposures.External,
	}
}

func (h *LoginHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{Body: dto.LoginBody{}}
}

func (h *LoginHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	body, _ := input.Body.(*dto.LoginBody)

	ip := clientIP(r)
	ua := r.UserAgent()
	res, err := h.usecases.Login(r.Context(), domain.LoginInput{
		Email: body.Email, Password: body.Password, IP: &ip, UserAgent: &ua,
	})
	if err != nil {
		return nil, err
	}

	h.cookie.set(w, res.SessionToken)
	return httpkit.Ok(dto.LoginResultToJSON(res)), nil
}
