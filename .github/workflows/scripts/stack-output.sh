#!/usr/bin/env bash
# Print the value of a CloudFormation output from one ZenTax cell.
#
#   usage: stack-output.sh <StackNamePrefix> <OutputKey> [<FallbackOutputKey>...]
#   e.g.   stack-output.sh ZenTax-StagingEu- ClusterName
#          (prefix = ZenTax-<Title>-, Title from env-title.sh: staging-eu -> StagingEu)
#
# The API repository owns exactly four stacks of a cell — <prefix>Network,
# <prefix>Data, <prefix>Cluster, <prefix>Api — and each is described BY NAME
# (`--stack-name`): an unnamed `describe-stacks` needs list permission on every
# stack in the account and hides which stack refused. (The UI repository's
# <prefix>Web / <prefix>Edge are never read here.) Rules:
#   * a stack that does not exist yet is skipped (before the first deploy the Api
#     stack is absent; that is normal);
#   * any other AWS error (AccessDenied, throttling, expired credentials, …) is
#     printed to stderr and fails the script — it is never swallowed;
#   * the first key that resolves wins; exit 1 with nothing on stdout if none does.
#
# STACK_OUTPUT_STACKS (space-separated stack suffixes) overrides the default list
# — for tests, or to pin a lookup to one stack.
set -euo pipefail

prefix="${1:?stack name prefix required}"
shift
[ "$#" -ge 1 ] || { echo "stack-output.sh: at least one output key required" >&2; exit 2; }

# shellcheck disable=SC2206 # deliberate word splitting of a space-separated list
stacks=(${STACK_OUTPUT_STACKS:-Network Data Cluster Api})

outputs='[]'
for suffix in "${stacks[@]}"; do
  stack="${prefix}${suffix}"
  err_file="$(mktemp)"
  if one="$(aws cloudformation describe-stacks --stack-name "$stack" \
      --query 'Stacks[0].Outputs' --output json 2>"$err_file")"; then
    rm -f "$err_file"
    outputs="$(jq -c --argjson add "${one:-null}" '. + ($add // [])' <<<"$outputs")"
  else
    err="$(cat "$err_file")"; rm -f "$err_file"
    if grep -q 'does not exist' <<<"$err"; then
      echo "stack-output.sh: stack $stack does not exist (yet) — skipped" >&2
      continue
    fi
    echo "stack-output.sh: describe-stacks --stack-name $stack failed:" >&2
    echo "$err" >&2
    exit 1
  fi
done

for key in "$@"; do
  value="$(jq -r --arg k "$key" '[.[] | select(.OutputKey == $k) | .OutputValue][0] // empty' <<<"$outputs")"
  if [ -n "$value" ]; then
    printf '%s\n' "$value"
    exit 0
  fi
done

echo "stack-output.sh: no output named $* in stacks ${prefix}{${stacks[*]// /,}}" >&2
exit 1
