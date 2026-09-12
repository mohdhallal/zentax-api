package pg

import (
	"context"

	"github.com/mohamadhallal/zentax-api/platform/authz"
	authzpg "github.com/mohamadhallal/zentax-api/platform/authz/pg"
	"github.com/mohamadhallal/zentax-api/platform/database"
	baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"
)

// entityObligationColumns is the domain projection — WITHOUT tenant_id (RLS
// infra, absent from the model). deadline_rule (JSONB) scans into the
// domain.DeadlineRule Scanner/Valuer type.
const entityObligationColumns = `id, entity_id, obligation_type_id, tax_reference_number, jurisdiction, ` +
	`jurisdiction_state, currency, periodicity, deadline_rule, status, created_at, updated_at, created_by, updated_by`

var sqlConfig = baserepo.SQLConfig{
	AllowedColumns: map[string]bool{
		"created_at":         true,
		"entity_id":          true,
		"obligation_type_id": true,
		"periodicity":        true,
		"status":             true,
		"jurisdiction":       true,
	},
	DefaultOrderBy:   "created_at",
	DefaultOrderDesc: true, // newest first
	GetById:          `SELECT ` + entityObligationColumns + ` FROM entity_obligations WHERE id = $1 LIMIT 1`,
	// tenant_id defaults from the GUC; status/deadline_rule default in DDL.
	Create: `
		INSERT INTO entity_obligations (entity_id, obligation_type_id, tax_reference_number, jurisdiction,
		                                jurisdiction_state, currency, periodicity, deadline_rule)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING ` + entityObligationColumns,
	Update: `
		UPDATE entity_obligations
		SET tax_reference_number = $2,
		    jurisdiction = $3,
		    jurisdiction_state = $4,
		    currency = $5,
		    periodicity = $6,
		    deadline_rule = $7,
		    status = $8,
		    updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING ` + entityObligationColumns,
	Delete:   `DELETE FROM entity_obligations WHERE id = $1`,
	Count:    `SELECT COUNT(*)::int AS total FROM entity_obligations`,
	ListBase: `SELECT ` + entityObligationColumns + ` FROM entity_obligations`,
}

// readScope narrows every read of this repository to the caller's entity
// subtree (ADR-0012 B-3). An obligation always names its entity, so the anchor
// is entity_id — unqualified, which the single-table statements and the
// GetById projection both accept.
func readScope(db database.ExecerPg) func(context.Context, func(any) string) (string, error) {
	return func(ctx context.Context, bind func(any) string) (string, error) {
		scope, err := authzpg.ReadScope(ctx, db, authz.EntityObligationRead)
		if err != nil {
			return "", err
		}
		return scope.EntityPredicate("entity_id", bind), nil
	}
}

// findByEntityAndTypeSQL resolves the payment rule for a workflow's (entity,
// obligation type) pair: active first, then the oldest registration.
const findByEntityAndTypeSQL = `
	SELECT ` + entityObligationColumns + `
	FROM entity_obligations
	WHERE entity_id = $1 AND obligation_type_id = $2
	ORDER BY (status = 'active') DESC, created_at ASC
	LIMIT 1`
