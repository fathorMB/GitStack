#!/usr/bin/env bash
# write|data: run the API's read/write endpoint in its pod; it connects to postgres by name.
# pg: GIT-514, the api app is gone by then, so read the marker with psql in the postgres app's own pod.
set -eu
if [ "$#" -ne 2 ]; then echo 'usage: pilot-db-probe.sh APP_ID write|data|pg' >&2; exit 2; fi
id=$1
case "$id" in *[!0-9a-f-]*|'') echo 'invalid app id' >&2; exit 2 ;; esac
case "$2" in write|data|pg) endpoint=$2 ;; *) echo 'invalid endpoint' >&2; exit 2 ;; esac
if [ "$endpoint" = pg ]; then
  value=$(sudo -n k3s kubectl exec -n "gitstack-app-$id" deploy/app -c app -- psql -X -A -t -v ON_ERROR_STOP=1 -U postgres -d postgres -c "SELECT value FROM pilot WHERE value='retail-pilot-381';") || { echo "STEP FAIL database-$endpoint: psql" >&2; exit 1; }
else
  value=$(sudo -n k3s kubectl exec -n "gitstack-app-$id" deploy/app -- node -e '
    fetch("http://127.0.0.1:8080/" + process.argv[1])
      .then(async r => { if (!r.ok) process.exit(1); console.log(await r.text()); })
      .catch(() => process.exit(1));' "$endpoint") || { echo "STEP FAIL database-$endpoint: API request" >&2; exit 1; }
fi
[ "$value" = 'retail-pilot-381' ] || { echo "STEP FAIL database-$endpoint: unexpected value" >&2; exit 1; }
echo "STEP ok database-$endpoint"
