-- Acceptance-owned contract schema.
--
-- Keep this intentionally smaller than the application schema: add only the
-- objects required to arrange and assert acceptance scenarios. This is not a
-- production migration and must not depend on the application's migration tool.

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE users (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email      VARCHAR(254) NOT NULL UNIQUE,
    name       VARCHAR(100) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE orders (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id),
    status       VARCHAR(20) NOT NULL DEFAULT 'pending',
    total_amount BIGINT NOT NULL,
    currency     VARCHAR(3) NOT NULL DEFAULT 'USD',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE payments (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id   UUID NOT NULL REFERENCES orders(id),
    status     VARCHAR(20) NOT NULL DEFAULT 'pending',
    amount     BIGINT NOT NULL,
    currency   VARCHAR(3) NOT NULL DEFAULT 'USD',
    method     VARCHAR(30) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE internal_api_keys (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_name    VARCHAR(100) NOT NULL,
    key         UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    secret_hash TEXT NOT NULL,
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
