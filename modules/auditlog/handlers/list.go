package handlers

import (
	"net/http"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/modules/auditlog/domain"
	"github.com/mohamadhallal/zentax-api/modules/auditlog/dto"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// ListAuditLogHandler serves GET /audit-log — the user-facing Audit Trail
// (ADR-0008 stream 1), newest first, gated by audit:read (reviewer / manager /
// tenant_admin). Read-only: the table itself is append-only at the database.
type ListAuditLogHandler struct {
	reader domain.Reader
}

func NewListAuditLogHandler(reader domain.Reader) *ListAuditLogHandler {
	return &ListAuditLogHandler{reader: reader}
}

func (h *ListAuditLogHandler) DefineRoute() types.RouteDefinition {
	return types.RouteDefinition{
		Method:     http.MethodGet,
		Path:       "/",
		Exposure:   types.Exposures.External,
		Tenant:     true,
		Capability: authz.AuditRead,
		Paginated:  true,
	}
}

func (h *ListAuditLogHandler) DefineSchema() types.SchemaDefinition {
	return types.SchemaDefinition{
		Query: dto.ListAuditLogQuery{},
	}
}

// DefineSortColumns declares no sortable keys: the trail has one order
// (seq DESC — the per-tenant ledger order, assigned under the same lock as
// occurred_at, so it is chronological too) so pages are stable.
func (h *ListAuditLogHandler) DefineSortColumns() map[string]string {
	return map[string]string{}
}

func (h *ListAuditLogHandler) Execute(
	w http.ResponseWriter, r *http.Request, input *types.ValidatedInput, requester *app.Requester,
) (*types.HttpResponse, error) {
	query, _ := input.Query.(*dto.ListAuditLogQuery)

	args := domain.ListArgs{
		WorkflowID:   query.WorkflowID,
		ResourceType: query.ResourceType,
		ResourceID:   query.ResourceID,
		Action:       query.Action,
		Limit:        query.Limit,
		Offset:       query.Offset,
	}
	var err error
	if args.From, err = parseDate(query.From); err != nil {
		return nil, err
	}
	if args.To, err = parseDate(query.To); err != nil {
		return nil, err
	}

	entries, total, err := h.reader.List(r.Context(), args)
	if err != nil {
		return nil, err
	}

	data := make([]map[string]any, 0, len(entries))
	for i := range entries {
		data = append(data, dto.EntryToJSON(&entries[i]))
	}

	return httpkit.Ok(data).WithPagination(types.Pagination{
		Total:  total,
		Limit:  args.Limit,
		Offset: args.Offset,
	}), nil
}

// parseDate turns an already layout-validated YYYY-MM-DD query value into a
// legal date (ADR-0002); nil stays nil.
func parseDate(s *string) (*dateonly.Date, error) {
	if s == nil {
		return nil, nil //nolint:nilnil // absent filter
	}
	d, err := dateonly.Parse(*s)
	if err != nil {
		return nil, err
	}
	return &d, nil
}
