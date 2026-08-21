-- Revert task_instance_approvals
BEGIN;

ALTER TABLE task_instances
    DROP COLUMN IF EXISTS submitted_by,
    DROP COLUMN IF EXISTS submitted_at,
    DROP COLUMN IF EXISTS rejection_reason;

COMMIT;
