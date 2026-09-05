#!/usr/bin/env bash
# Resolve the exact task-definition revision a one-off job (migrate / seed) will
# run, starting from the revision the Cluster stack exported — never from the
# bare family, whose ":latest" revision could have been registered by anyone
# with ecs:RegisterTaskDefinition.
#
#   usage: ecs-pin-taskdef.sh <pinned task-definition ARN> <image tag>
#   env:   AWS_ACCOUNT_ID   account the cell lives in
#          TARGET_ENV       the cell name: staging-eu | production-eu
#   stdout: the ARN to pass to `ecs run-task`
#
# Steps:
#   1. describe the pinned revision (the ARN carries ":<revision>");
#   2. VERIFY it: taskRoleArn and executionRoleArn must both be
#      arn:aws:iam::<account>:role/zentax-<cell>-* and every container image must
#      come from this account's ECR registry — otherwise fail before run-task;
#   3. if the image already carries <image tag> (the normal case: the Cluster
#      stack was just deployed with --context imageTag), print the pinned ARN;
#      otherwise register ONE new revision that is the pinned one with only the
#      image tag swapped (roles, secrets, network, logging untouched) and print
#      that ARN. Nothing from the definition is echoed except ARNs and images.
set -euo pipefail

pinned="${1:?pinned task-definition ARN required}"
tag="${2:?image tag required}"
: "${AWS_ACCOUNT_ID:?AWS_ACCOUNT_ID required}"
: "${TARGET_ENV:?TARGET_ENV required}"

case "$pinned" in
  arn:aws:ecs:*:"${AWS_ACCOUNT_ID}":task-definition/zentax-"${TARGET_ENV}"-*:[0-9]*) ;;
  *)
    echo "::error title=task definition::'$pinned' is not a pinned revision ARN of a zentax-${TARGET_ENV}-* family in account ${AWS_ACCOUNT_ID}" >&2
    exit 1;;
esac

td="$(aws ecs describe-task-definition --task-definition "$pinned" --query taskDefinition --output json)"

role_prefix="arn:aws:iam::${AWS_ACCOUNT_ID}:role/zentax-${TARGET_ENV}-"
for field in taskRoleArn executionRoleArn; do
  role="$(jq -r --arg f "$field" '.[$f] // empty' <<<"$td")"
  case "$role" in
    "${role_prefix}"*) ;;
    *)
      echo "::error title=task definition::$pinned: $field is '${role:-<unset>}', expected ${role_prefix}* — refusing to run it" >&2
      exit 1;;
  esac
done

registry="${AWS_ACCOUNT_ID}.dkr.ecr."
bad_image="$(jq -r --arg r "$registry" '[.containerDefinitions[].image | select(startswith($r) | not)][0] // empty' <<<"$td")"
if [ -n "$bad_image" ]; then
  echo "::error title=task definition::$pinned: image '$bad_image' is not from this account's ECR registry — refusing to run it" >&2
  exit 1
fi

current="$(jq -r '.containerDefinitions[0].image' <<<"$td")"
wanted="$(sed -E "s/:[^:/]+$/:${tag}/" <<<"$current")"
if [ "$current" = "$wanted" ]; then
  echo "pinned revision already runs image tag $tag: $pinned" >&2
  printf '%s\n' "$pinned"
  exit 0
fi

new_td="$(jq --arg tag "$tag" '
  del(.taskDefinitionArn, .revision, .status, .requiresAttributes,
      .compatibilities, .registeredAt, .registeredBy, .deregisteredAt)
  | .containerDefinitions |= map(.image |= sub(":[^:/]+$"; ":" + $tag))
' <<<"$td")"
new_arn="$(aws ecs register-task-definition --cli-input-json "$new_td" \
  --query taskDefinition.taskDefinitionArn --output text)"
echo "registered $new_arn from $pinned (image $current -> $wanted)" >&2
printf '%s\n' "$new_arn"
