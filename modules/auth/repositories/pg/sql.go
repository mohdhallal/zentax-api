package pg

var internalAPIKeySQL = struct {
	GetByKey string
}{
	GetByKey: `
		SELECT id, app_name, key, secret_hash, active, created_at, updated_at
		FROM internal_api_keys
		WHERE key = $1 AND active = true
		LIMIT 1
	`,
}

var nexusAccountAPIKeySQL = struct {
	Store               string
	GetByNexusAccountID string
}{
	Store: `
		INSERT INTO nexus_accounts_api_keys (nexus_account_id, api_key, api_secret)
		VALUES ($1, $2, $3)
		RETURNING id, nexus_account_id, api_key, api_secret, created_at, updated_at
	`,
	GetByNexusAccountID: `
		SELECT id, nexus_account_id, api_key, api_secret, created_at, updated_at
		FROM nexus_accounts_api_keys
		WHERE nexus_account_id = $1
		LIMIT 1
	`,
}
