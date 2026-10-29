#!/usr/bin/env bash
# Verify snapshots returned by the workflow runs and packages APIs for the pilot.
# Usage: pilot-ci-check.sh RUNS.json PACKAGES.json TAG COMMIT
set -eu

if [ "$#" -ne 4 ]; then
  echo 'usage: pilot-ci-check.sh RUNS.json PACKAGES.json TAG COMMIT' >&2
  exit 2
fi
runs=$1
packages=$2
tag=$3
commit=$4

fail() { printf 'STEP FAIL %s\n' "$1" >&2; exit 1; }

# Match the tag ref as well as its commit: a branch push can have the same SHA.
if ! jq -e --arg ref "refs/tags/$tag" --arg sha "$commit" '
  [.items[] | select(.event == "push" and .ref == $ref and .sha == $sha
    and .status == "completed" and .conclusion == "success"
    and any(.jobs[]; .runnerType == "internal_pod" and .status == "completed" and .conclusion == "success"))]
  | length > 0' "$runs" >/dev/null; then
  fail 'run del tag non riuscito sul runner integrato'
fi
echo 'STEP ok run-tag'

for name in api sito db; do
  if ! jq -e --arg name "$name" --arg tag "$tag" '
    [.items[] | select(.name == $name) | .tagDetails[]?
      | select(.name == $tag and .signature == "verified" and .pushedBy.kind == "job"
        and (.digest | test("^sha256:[0-9a-f]{64}$")))] | length > 0' "$packages" >/dev/null; then
    fail "immagine $name:$tag non firmata dal job"
  fi
  printf 'STEP ok image-%s\n' "$name"
done
