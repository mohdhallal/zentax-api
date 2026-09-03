-- Deploy entity_obligation_details
BEGIN;

-- Entity-obligation registration details carried over from the legacy
-- (spreadsheet-era) shape: the tax reference number the authority issued, a
-- sub-national jurisdiction (state/province) and the obligation currency.
-- `jurisdiction` stays the COUNTRY; `jurisdiction_state` narrows it. The
-- deadline builder itself (period start, filing/payment offsets, fixed dates,
-- additional deadlines) lives in the deadline_rule JSONB — rules are versioned
-- data, not columns (ADR-0017) — so nothing about deadlines is added here.
-- Additive, nullable columns on an existing HASH(tenant_id) partitioned table:
-- ADD COLUMN propagates to every partition and takes no rewrite (ADR-0013
-- expand step; ADR-0020 needs no new partitioning).
ALTER TABLE entity_obligations
    ADD COLUMN tax_reference_number TEXT,
    ADD COLUMN jurisdiction_state TEXT,
    ADD COLUMN currency VARCHAR(3); -- ISO 4217 alpha-3, upper-cased by the API

COMMIT;
