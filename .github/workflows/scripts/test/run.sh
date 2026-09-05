#!/usr/bin/env bash
# Tests for the deploy helper scripts, against the fake `aws` in test/bin.
# No credentials, no network:  bash .github/workflows/scripts/test/run.sh
#
# Assertions are single-quoted expressions evaluated by `check` after the
# command under test ran (so $out / $rc are read then, not at parse time) —
# the linter cannot see those reads, hence SC2016/SC2034 are off for this file.
# shellcheck disable=SC2016,SC2034
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
scripts="$(dirname "$here")"
export PATH="$here/bin:$PATH"
export AWS_ACCOUNT_ID=160117555326 TARGET_ENV=staging
export AWS_ACCESS_KEY_ID=fake AWS_SECRET_ACCESS_KEY=fake AWS_DEFAULT_REGION=eu-central-1

pass=0; fail=0
ok()   { pass=$((pass + 1)); echo "  ok   $1"; }
bad()  { fail=$((fail + 1)); echo "  FAIL $1"; }
check() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }

fresh() { # new fixture dir
  FAKE_AWS_DIR="$(mktemp -d)"; export FAKE_AWS_DIR
  mkdir -p "$FAKE_AWS_DIR/stacks" "$FAKE_AWS_DIR/taskdefs"
  : > "$FAKE_AWS_DIR/calls.log"
}
stack() { # <name> <json outputs array>
  printf '%s' "$2" > "$FAKE_AWS_DIR/stacks/$1.json"
}
ROLE_OK='arn:aws:iam::160117555326:role/zentax-staging-migrate'
IMG='160117555326.dkr.ecr.eu-central-1.amazonaws.com/zentax/staging/migrate'
taskdef() { # <name:rev> <image> <taskRole> <execRole>
  jq -n --arg img "$2" --arg tr "$3" --arg er "$4" --arg fam "${1%%:*}" --arg arn "arn:aws:ecs:eu-central-1:160117555326:task-definition/$1" '{
    taskDefinitionArn: $arn, family: $fam, revision: 3, status: "ACTIVE",
    taskRoleArn: $tr, executionRoleArn: $er, networkMode: "awsvpc",
    requiresCompatibilities: ["FARGATE"], cpu: "256", memory: "512",
    requiresAttributes: [{name: "x"}], compatibilities: ["FARGATE"], registeredAt: 1, registeredBy: "cdk",
    containerDefinitions: [{name: "migrate", image: $img, essential: true,
      secrets: [{name: "PGPASSWORD", valueFrom: "arn:aws:secretsmanager:eu-central-1:160117555326:secret:zentax/staging/db-master:password::"}]}]
  }' > "$FAKE_AWS_DIR/taskdefs/$1.json"
}
ARN3='arn:aws:ecs:eu-central-1:160117555326:task-definition/zentax-staging-migrate:3'

echo "stack-output.sh"
fresh
stack ZenTax-Staging-Network '[{"OutputKey":"PrivateSubnetIds","OutputValue":"subnet-a,subnet-b"}]'
stack ZenTax-Staging-Data    '[{"OutputKey":"ApiRepositoryUri","OutputValue":"160117555326.dkr.ecr.eu-central-1.amazonaws.com/zentax/staging/api"}]'
stack ZenTax-Staging-Cluster '[{"OutputKey":"ClusterName","OutputValue":"zentax-staging"},{"OutputKey":"MigrateTaskDefinitionArn","OutputValue":"'"$ARN3"'"}]'
# Api deliberately absent: first deploy
out="$("$scripts/stack-output.sh" ZenTax-Staging- MigrateTaskDefinitionArn 2>"$FAKE_AWS_DIR/err")"
check "resolves a Cluster output while Api does not exist yet" '[ "$out" = "$ARN3" ]'
check "reports the missing Api stack as skipped, not as an error" 'grep -q "ZenTax-Staging-Api does not exist" "$FAKE_AWS_DIR/err"'
check "describes each of the four API-owned stacks by name" \
  'for s in Network Data Cluster Api; do grep -q -- "--stack-name ZenTax-Staging-$s" "$FAKE_AWS_DIR/calls.log" || exit 1; done'
check "never touches the UI repository's Web / Edge / App stacks" '! grep -qE -- "--stack-name ZenTax-Staging-(Web|Edge|App)" "$FAKE_AWS_DIR/calls.log"'
check "never issues an unnamed describe-stacks" '! grep -v -- "--stack-name" "$FAKE_AWS_DIR/calls.log" | grep -q describe-stacks'
out="$("$scripts/stack-output.sh" ZenTax-Staging- NoSuchKey ClusterName 2>/dev/null)"
check "falls back through the key list in order" '[ "$out" = "zentax-staging" ]'
rc=0; out="$("$scripts/stack-output.sh" ZenTax-Staging- NoSuchKey 2>"$FAKE_AWS_DIR/err")" || rc=$?
check "exits 1 with nothing on stdout when no key resolves" '[ "$rc" = 1 ] && [ -z "$out" ]'
# an output that lives in Api, once Api exists
stack ZenTax-Staging-Api '[{"OutputKey":"ApiServiceName","OutputValue":"zentax-staging-api"}]'
out="$("$scripts/stack-output.sh" ZenTax-Staging- ApiServiceName 2>/dev/null)"
check "resolves an Api output once Api exists" '[ "$out" = "zentax-staging-api" ]'
# AccessDenied must never be swallowed
echo "An error occurred (AccessDenied) when calling the DescribeStacks operation: User: arn:aws:sts::160117555326:assumed-role/zentax-deploy-staging-api/x is not authorized to perform: cloudformation:DescribeStacks" \
  > "$FAKE_AWS_DIR/stacks/ZenTax-Staging-Data.error"
rc=0; out="$("$scripts/stack-output.sh" ZenTax-Staging- ClusterName 2>"$FAKE_AWS_DIR/err")" || rc=$?
check "fails on AccessDenied even though another stack has the key" '[ "$rc" = 1 ] && [ -z "$out" ]'
check "prints the AWS error to stderr" 'grep -q "AccessDenied" "$FAKE_AWS_DIR/err" && grep -q "ZenTax-Staging-Data" "$FAKE_AWS_DIR/err"'
rm -rf "$FAKE_AWS_DIR"

echo "ecs-pin-taskdef.sh"
fresh; taskdef zentax-staging-migrate:3 "$IMG:abc-def" "$ROLE_OK-task" "$ROLE_OK-exec"
out="$("$scripts/ecs-pin-taskdef.sh" "$ARN3" abc-def 2>/dev/null)"
check "returns the pinned ARN when it already carries the tag" '[ "$out" = "$ARN3" ]'
check "registers nothing in that case" '! grep -q register-task-definition "$FAKE_AWS_DIR/calls.log"'

fresh; taskdef zentax-staging-migrate:3 "$IMG:old-old" "$ROLE_OK-task" "$ROLE_OK-exec"
out="$("$scripts/ecs-pin-taskdef.sh" "$ARN3" new-new 2>/dev/null)"
check "registers one revision when the tag differs and returns its ARN" '[ "$out" = "arn:aws:ecs:eu-central-1:160117555326:task-definition/zentax-staging-migrate:99" ]'
check "the new revision runs the new image tag" '[ "$(jq -r .containerDefinitions[0].image "$FAKE_AWS_DIR/registered.json")" = "$IMG:new-new" ]'
check "the new revision keeps the task and execution roles" \
  '[ "$(jq -r .taskRoleArn "$FAKE_AWS_DIR/registered.json")" = "$ROLE_OK-task" ] && [ "$(jq -r .executionRoleArn "$FAKE_AWS_DIR/registered.json")" = "$ROLE_OK-exec" ]'
check "the new revision keeps the secrets block" '[ "$(jq -r .containerDefinitions[0].secrets[0].name "$FAKE_AWS_DIR/registered.json")" = "PGPASSWORD" ]'
check "read-only describe fields are stripped before register" \
  '[ "$(jq -r "[.taskDefinitionArn,.revision,.status,.requiresAttributes,.compatibilities,.registeredAt,.registeredBy] | map(. == null) | all" "$FAKE_AWS_DIR/registered.json")" = true ]'

fresh; taskdef zentax-staging-migrate:3 "$IMG:abc-def" "arn:aws:iam::160117555326:role/AdministratorAccess" "$ROLE_OK-exec"
rc=0; out="$("$scripts/ecs-pin-taskdef.sh" "$ARN3" abc-def 2>"$FAKE_AWS_DIR/err")" || rc=$?
check "refuses a task role outside zentax-<env>-*" '[ "$rc" = 1 ] && [ -z "$out" ] && grep -q taskRoleArn "$FAKE_AWS_DIR/err"'
check "does not register or run anything after a role mismatch" '! grep -q register-task-definition "$FAKE_AWS_DIR/calls.log"'

fresh; taskdef zentax-staging-migrate:3 "$IMG:abc-def" "$ROLE_OK-task" "arn:aws:iam::999999999999:role/zentax-staging-migrate-exec"
rc=0; out="$("$scripts/ecs-pin-taskdef.sh" "$ARN3" abc-def 2>"$FAKE_AWS_DIR/err")" || rc=$?
check "refuses an execution role from another account" '[ "$rc" = 1 ] && grep -q executionRoleArn "$FAKE_AWS_DIR/err"'

fresh; taskdef zentax-staging-migrate:3 "docker.io/library/postgres:16" "$ROLE_OK-task" "$ROLE_OK-exec"
rc=0; out="$("$scripts/ecs-pin-taskdef.sh" "$ARN3" abc-def 2>"$FAKE_AWS_DIR/err")" || rc=$?
check "refuses an image outside this account's ECR" '[ "$rc" = 1 ] && grep -q "ECR registry" "$FAKE_AWS_DIR/err"'

fresh; taskdef zentax-staging-migrate:3 "$IMG:abc-def" "$ROLE_OK-task" "$ROLE_OK-exec"
rc=0; out="$("$scripts/ecs-pin-taskdef.sh" zentax-staging-migrate abc-def 2>"$FAKE_AWS_DIR/err")" || rc=$?
check "refuses a bare family (unpinned) instead of an ARN" '[ "$rc" = 1 ] && ! grep -q describe-task-definition "$FAKE_AWS_DIR/calls.log"'
rc=0; out="$("$scripts/ecs-pin-taskdef.sh" "arn:aws:ecs:eu-central-1:160117555326:task-definition/zentax-production-migrate:3" abc-def 2>"$FAKE_AWS_DIR/err")" || rc=$?
check "refuses a task definition of another environment" '[ "$rc" = 1 ] && [ -z "$out" ]'
rm -rf "$FAKE_AWS_DIR"

echo
echo "$pass passed, $fail failed"
[ "$fail" = 0 ]
