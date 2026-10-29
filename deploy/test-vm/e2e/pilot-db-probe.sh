#!/usr/bin/env bash
# Run the API's read/write endpoint in its pod; it connects to postgres by name.
set -eu
if [ "$#" -ne 2 ]; then echo 'usage: pilot-db-probe.sh APP_ID write|data' >&2; exit 2; fi
id=$1
case "$id" in *[!0-9a-f-]*|'') echo 'invalid app id' >&2; exit 2 ;; esac
case "$2" in write|data) endpoint=$2 ;; *) echo 'invalid endpoint' >&2; exit 2 ;; esac
value=$(sudo -n k3s kubectl exec -n "gitstack-app-$id" deploy/app -- node -e '
  fetch("http://127.0.0.1:8080/" + process.argv[1])
    .then(async r => { if (!r.ok) process.exit(1); console.log(await r.text()); })
    .catch(() => process.exit(1));' "$endpoint") || { echo "STEP FAIL database-$endpoint: API request" >&2; exit 1; }
[ "$value" = 'retail-pilot-381' ] || { echo "STEP FAIL database-$endpoint: unexpected value" >&2; exit 1; }
echo "STEP ok database-$endpoint"
