-- Verify task_instance_approvals
SELECT submitted_by, submitted_at, rejection_reason FROM task_instances WHERE false;
