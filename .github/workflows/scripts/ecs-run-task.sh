#!/usr/bin/env bash
# Run a one-shot ECS Fargate task (migrate / seed), wait for it, fail on a
# non-zero container exit code, and print the CloudWatch log tail.
#
#   usage: ecs-run-task.sh <task-definition family|ARN> [overrides.json]
#   env:   CLUSTER          ECS cluster name
#          SUBNETS          comma-separated private subnet ids
#          SECURITY_GROUP   task security group id
#          LOG_LINES        how many log events to tail (default 200)
#          WAIT_ROUNDS      `aws ecs wait tasks-stopped` rounds of ~10 min (default 3)
#
# Secret hygiene (ADR-0014/0015): nothing from the task definition, the overrides
# file or the environment is echoed — only ARNs, exit codes, stop reasons and
# the container's own log lines (the migrate runner never prints credentials).
set -euo pipefail

taskdef="${1:?task definition family or ARN required}"
overrides_file="${2:-}"
: "${CLUSTER:?CLUSTER required}"
: "${SUBNETS:?SUBNETS required}"
: "${SECURITY_GROUP:?SECURITY_GROUP required}"
LOG_LINES="${LOG_LINES:-200}"
WAIT_ROUNDS="${WAIT_ROUNDS:-3}"

subnets_json="$(jq -cR 'split(",") | map(select(length > 0))' <<<"$SUBNETS")"
netcfg="$(jq -cn --argjson s "$subnets_json" --arg sg "$SECURITY_GROUP" \
  '{awsvpcConfiguration: {subnets: $s, securityGroups: [$sg], assignPublicIp: "DISABLED"}}')"

run_args=(--cluster "$CLUSTER" --launch-type FARGATE --task-definition "$taskdef"
          --network-configuration "$netcfg" --count 1
          --started-by "github-actions:${GITHUB_RUN_ID:-local}")
if [ -n "$overrides_file" ]; then
  run_args+=(--overrides "file://${overrides_file}")
fi

echo "run-task: $taskdef on cluster $CLUSTER"
run_out="$(aws ecs run-task "${run_args[@]}" --output json)"
failures="$(jq -r '.failures // [] | map("\(.arn // "-"): \(.reason // "?") \(.detail // "")") | join("; ")' <<<"$run_out")"
task_arn="$(jq -r '.tasks[0].taskArn // empty' <<<"$run_out")"
if [ -z "$task_arn" ]; then
  echo "::error title=ecs run-task::no task started — ${failures:-unknown failure}"
  exit 1
fi
task_id="${task_arn##*/}"
echo "task: $task_arn"

# The waiter gives up after ~10 min; migrations on a large DB may need more.
stopped=0
for _ in $(seq 1 "$WAIT_ROUNDS"); do
  if aws ecs wait tasks-stopped --cluster "$CLUSTER" --tasks "$task_arn"; then
    stopped=1
    break
  fi
  echo "still running, waiting more..."
done
if [ "$stopped" != "1" ]; then
  echo "::error title=ecs task::task $task_id did not stop within the allowed time — inspect it in the console"
  exit 1
fi

desc="$(aws ecs describe-tasks --cluster "$CLUSTER" --tasks "$task_arn" --output json)"
exit_code="$(jq -r '.tasks[0].containers[0].exitCode // "none"' <<<"$desc")"
stop_reason="$(jq -r '.tasks[0].stoppedReason // "-"' <<<"$desc")"
container_reason="$(jq -r '.tasks[0].containers[0].reason // "-"' <<<"$desc")"
container_name="$(jq -r '.tasks[0].containers[0].name' <<<"$desc")"
echo "stopped: exitCode=$exit_code reason=\"$stop_reason\" container=\"$container_reason\""

# Log tail: the awslogs stream name is <prefix>/<container>/<task-id>.
td_arn="$(jq -r '.tasks[0].taskDefinitionArn' <<<"$desc")"
logcfg="$(aws ecs describe-task-definition --task-definition "$td_arn" \
  --query "taskDefinition.containerDefinitions[?name=='${container_name}'].logConfiguration | [0]" --output json)"
log_group="$(jq -r '.options["awslogs-group"] // empty' <<<"$logcfg")"
log_prefix="$(jq -r '.options["awslogs-stream-prefix"] // empty' <<<"$logcfg")"
if [ -n "$log_group" ] && [ -n "$log_prefix" ]; then
  stream="${log_prefix}/${container_name}/${task_id}"
  echo "::group::log tail ($log_group :: $stream)"
  aws logs get-log-events --log-group-name "$log_group" --log-stream-name "$stream" \
    --limit "$LOG_LINES" --no-start-from-head --query 'events[].message' --output text 2>/dev/null \
    | tr '\t' '\n' || echo "(no log events yet — the stream may still be flushing)"
  echo "::endgroup::"
else
  echo "(task definition has no awslogs configuration; skipping log tail)"
fi

if [ "$exit_code" != "0" ]; then
  echo "::error title=ecs task::$taskdef exited with code $exit_code"
  exit 1
fi
echo "ok: $taskdef completed"
