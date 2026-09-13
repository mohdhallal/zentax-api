-- Deploy audit_credential
BEGIN;

-- ADR-0008, the join between the two streams.
--
-- security_events records that a session began, from which address, after which
-- refusals. audit_log records what then changed. Until now NOTHING joined them:
-- the envelope named the PRINCIPAL (actor_id) and never the CREDENTIAL, so an
-- investigator holding a suspicious session could not ask what that session did.
-- Two live sessions of one account — the owner's and an attacker's — were the
-- same actor in the trail, and the machine case was worse: api_token.issued
-- names a token, but no later entry said which token made a change.
-- request_id does not bridge it either; a login and a later mutation are
-- different requests.
--
-- So every entry now names the credential it was made with:
--
--   credential_id   UUID        — sessions.id, or api_tokens.id
--   credential_kind VARCHAR(16) — which of the two tables that id lives in
--
-- Both NULL together for an entry written by a principal that authenticated
-- with neither (the export scheduler's system actor). No foreign key on
-- purpose: sessions and api_tokens are erasable/expiring operational rows,
-- audit_log is a decade-long ledger, and a reference that RESTRICTs their
-- deletion — or CASCADEs into the ledger — is the last thing either wants. The
-- id is evidence, not a live pointer.
--
-- WHY IT IS AN ENVELOPE COLUMN AND NOT A `details` KEY: the credential is
-- envelope-shaped (every entry has one, it is not about the resource), it must
-- be queryable and indexable for the one question an incident review asks, and
-- it must be covered by the hash chain, which `details` would also give but at
-- the cost of burying a join key inside a per-action payload.
--
-- WHY IT IS NOT A PII PROBLEM IN A TEN-YEAR WORM STORE: the value is a
-- server-minted random UUID with no content about a person, in the same class
-- as actor_id and event_id, which the envelope already carries. It is
-- pseudonymous linkage data, not a new category of personal datum; the
-- credential -> person mapping lives in `sessions` / `api_tokens`, which are
-- erasable and expire, so once those rows are gone an exported id is an opaque
-- token. That is the ADR-0007 reasoning that already admitted actor_id, and it
-- is the reason the SUBMITTED ADDRESS on the security stream got a keyed digest
-- instead: an address identifies a person by itself, a session id does not.
ALTER TABLE audit_log ADD COLUMN credential_id   UUID;
ALTER TABLE audit_log ADD COLUMN credential_kind VARCHAR(16);

-- Present together or absent together: half a credential is a join key pointing
-- at no table. Validated, not NOT VALID — both columns are new, so every
-- existing row is NULL/NULL and the constraint is true of the whole table.
ALTER TABLE audit_log ADD CONSTRAINT audit_log_credential_pair
    CHECK ((credential_id IS NULL) = (credential_kind IS NULL));

-- A closed vocabulary, so the column can never become free text: it names which
-- store the id belongs to, and there are exactly two.
ALTER TABLE audit_log ADD CONSTRAINT audit_log_credential_kind
    CHECK (credential_kind IS NULL OR credential_kind IN ('session', 'api_token'));

-- The incident-review query: "everything this credential changed, newest
-- first". Partial, because most rows of a quiet system have no credential.
CREATE INDEX idx_audit_log_credential ON audit_log (tenant_id, credential_id, seq DESC)
    WHERE credential_id IS NOT NULL;

COMMENT ON COLUMN audit_log.credential_id IS
    'Credential the actor authenticated with: sessions.id or api_tokens.id (see credential_kind). The join to security_events.session_id, which names the same ids. NULL when the principal held neither (scheduled work). No FK: this is evidence, not a live reference.';
COMMENT ON COLUMN audit_log.credential_kind IS
    'Which store credential_id belongs to: session | api_token. NULL exactly when credential_id is NULL.';

-- The hashing contract this adds: platform/audit writes hash_version 3, whose
-- canonical form is version 2's with credential_kind and credential_id appended
-- (empty strings when absent). Versions EXTEND, and ComputeHash renders the
-- version each ROW carries, so every version-2 entry keeps verifying exactly as
-- it did — a cut-over that quietly downgraded older rows to "link-checked only"
-- would delete the product's tamper evidence with a one-line constant.
COMMENT ON COLUMN audit_log.hash_version IS
    'Hashing contract the row was written under: 1 = pre-cut-over (occurred_at hashed at nanosecond precision, not recomputable from this row), 2 = hashed at the column''s microsecond resolution, 3 = 2 plus the acting credential (kind, then id) appended to the canonical form. Written explicitly by platform/audit; DEFAULT 1 so an unaware writer is recorded as unverifiable.';

COMMIT;
