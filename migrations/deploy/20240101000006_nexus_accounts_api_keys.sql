-- Deploy nexus_accounts_api_keys
BEGIN;

CREATE TABLE nexus_accounts_api_keys (
    id               SERIAL PRIMARY KEY,
    nexus_account_id INT NOT NULL,
    api_key          TEXT NOT NULL,
    api_secret       TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_nexus_accounts_api_keys_nexus_account_id ON nexus_accounts_api_keys(nexus_account_id);

COMMIT;
