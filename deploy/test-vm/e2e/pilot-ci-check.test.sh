#!/usr/bin/env bash
set -eu
dir=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
sha=0123456789abcdef0123456789abcdef01234567
digest=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
cat >"$tmp/runs.json" <<EOF
{"items":[{"event":"push","ref":"refs/tags/v1","sha":"$sha","status":"completed","conclusion":"success","jobs":[{"runnerType":"internal_pod","status":"completed","conclusion":"success"}]}]}
EOF
cat >"$tmp/packages.json" <<EOF
{"items":[{"name":"api","tagDetails":[{"name":"v1","digest":"sha256:$digest","signature":"verified","pushedBy":{"kind":"job"}}]},{"name":"sito","tagDetails":[{"name":"v1","digest":"sha256:$digest","signature":"verified","pushedBy":{"kind":"job"}}]}]}
EOF
"$dir/pilot-ci-check.sh" "$tmp/runs.json" "$tmp/packages.json" v1 "$sha" >/dev/null
if "$dir/pilot-ci-check.sh" "$tmp/runs.json" "$tmp/packages.json" v2 "$sha" >/dev/null 2>&1; then echo 'wrong tag accepted' >&2; exit 1; fi
jq '.items[1].tagDetails[0].signature = "unverified"' "$tmp/packages.json" >"$tmp/unsigned.json"
if "$dir/pilot-ci-check.sh" "$tmp/runs.json" "$tmp/unsigned.json" v1 "$sha" >/dev/null 2>&1; then echo 'unsigned image accepted' >&2; exit 1; fi
jq '.items[0].ref = "refs/heads/main"' "$tmp/runs.json" >"$tmp/branch.json"
if "$dir/pilot-ci-check.sh" "$tmp/branch.json" "$tmp/packages.json" v1 "$sha" >/dev/null 2>&1; then echo 'branch run accepted' >&2; exit 1; fi
echo 'pilot CI checks: OK'
