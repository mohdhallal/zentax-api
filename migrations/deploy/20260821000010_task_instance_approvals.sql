-- Deploy task_instance_approvals
BEGIN;

-- Approval-flow columns (ADR-0018 attestation + ADR-0012 segregation of duties):
-- who submitted a task instance for approval and when, and the reason if the
-- reviewer rejected it. approved_by / approved_at / completed_at already exist.
-- submitted_by lets the server enforce approver ≠ submitter (preparer ≠ approver).
ALTER TABLE task_instances
    ADD COLUMN submitted_by     UUID,
    ADD COLUMN submitted_at     TIMESTAMPTZ,
    ADD COLUMN rejection_reason TEXT;

COMMIT;
