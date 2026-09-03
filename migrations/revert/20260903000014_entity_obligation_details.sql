-- Revert entity_obligation_details
BEGIN;

ALTER TABLE entity_obligations
    DROP COLUMN IF EXISTS tax_reference_number,
    DROP COLUMN IF EXISTS jurisdiction_state,
    DROP COLUMN IF EXISTS currency;

COMMIT;
