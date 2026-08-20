-- Verify appschema
SELECT 1/count(*) FROM pg_extension WHERE extname = 'pgcrypto';
