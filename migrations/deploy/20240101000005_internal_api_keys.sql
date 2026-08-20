-- Deploy internal_api_keys
BEGIN;

CREATE TABLE internal_api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_name VARCHAR(100) NOT NULL,
    key UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    secret_hash TEXT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_internal_api_keys_key ON internal_api_keys(key);
CREATE INDEX idx_internal_api_keys_app_name ON internal_api_keys(app_name);

COMMIT;
