package pg

// dataTemplateColumns is the domain projection — WITHOUT tenant_id (RLS infra,
// absent from the model). fields (JSONB) scans into domain.Fields.
const dataTemplateColumns = `id, name, template_type, category, description, fields, created_at, updated_at, created_by, updated_by`

const (
	getByIdSQL = `SELECT ` + dataTemplateColumns + ` FROM data_templates WHERE id = $1 LIMIT 1`

	// tenant_id / created_by default from the GUCs bound at the Tx seam.
	createSQL = `
		INSERT INTO data_templates (name, template_type, category, description, fields)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + dataTemplateColumns

	updateSQL = `
		UPDATE data_templates
		SET name = $2,
		    template_type = $3,
		    description = $4,
		    fields = $5,
		    updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING ` + dataTemplateColumns

	deleteSQL = `DELETE FROM data_templates WHERE id = $1`

	// Not paginated: a tenant's template registry is small. NULL filter = any.
	listSQL = `
		SELECT ` + dataTemplateColumns + `
		FROM data_templates
		WHERE ($1::varchar IS NULL OR template_type = $1)
		  AND ($2::varchar IS NULL OR category = $2)
		ORDER BY name ASC, id ASC`

	// Both referencing tables are RLS-scoped, so this counts the tenant's own
	// references only (the composite FK guarantees there are no others).
	referenceCountSQL = `
		SELECT (SELECT COUNT(*) FROM workflow_tasks WHERE data_template_id = $1)::int
		     + (SELECT COUNT(*) FROM task_instances WHERE data_template_id = $1)::int`
)
