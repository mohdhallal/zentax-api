#!/usr/bin/env bash
# The PascalCase title of a cell name — the `ZenTax-<Title>-*` stack-name
# segment the CDK app derives from `--context env=<cell>` (infra/lib/config.ts,
# envTitle): staging-eu -> StagingEu, production-eu -> ProductionEu.
#
#   usage: env-title.sh <cell>      (cell = <staging|production>-<two-letter region label>)
#   stdout: the title; exit 1 (nothing on stdout) for anything that is not a cell name
set -euo pipefail

name="${1:?cell name required, e.g. staging-eu}"
case "$name" in
  staging-[a-z][a-z]|production-[a-z][a-z]) ;;
  *)
    echo "env-title.sh: '$name' is not a cell name (<staging|production>-<two-letter region label>, e.g. staging-eu)" >&2
    exit 1;;
esac
# Portable (no bash 4 ${var^} / GNU sed \U): capitalise each dash-separated part and join.
awk -F- '{ for (i = 1; i <= NF; i++) printf "%s%s", toupper(substr($i, 1, 1)), substr($i, 2); print "" }' <<<"$name"
