---
id: upgrade
title: Upgrade
---

`gitstack upgrade` updates the chart, the images and the admin binary, takes a
preventive backup first, waits for the services to become healthy and rolls
back by itself if they do not. Read the [versioning policy](./versioning.md)
for the supported upgrade paths: you can upgrade directly from a minor
version to any later one.

## Procedure

```sh
sudo gitstack upgrade --version vX.Y.Z --dry-run   # checks only, changes nothing
sudo gitstack upgrade --version vX.Y.Z
sudo gitstack status
```

Without `--version` the target is the latest build of `main`; `--to TAG|SHA`
selects a specific build. Useful options: `--key-file` (encrypt the preventive
backup), `--dest`, `--timeout` (default 10 minutes), `--set`, `--values`.

Order of steps: checks (no change if refused) → preventive backup → k3s (only
if the target declares another version) → Helm upgrade and health wait → local
replacement of the binary, the chart copy and the image tag.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Done |
| `6` | Refused (downgrade, incompatible target, unhealthy now, missing checksum or signature): nothing changed |
| `7` | The upgrade failed and the previous state was restored automatically |
| `8` | The upgrade and the rollback both failed: manual intervention (the message tells how) |
| `70` | Unexpected error, for example in the k3s step: the command prints the path of the preventive backup |

On a failed health check the rollback restores the previous binary and chart,
runs `helm rollback` and, if migrations touched the database, restores the
preventive backup (data written after it is lost).

## k3s

Each GitStack version declares the k3s version it was tested with
(`deploy/k3s-version`). `gitstack upgrade` compares it with the running one:

| Change | Behaviour |
|---|---|
| Patch (or a new `+k3sN` revision) | Updated automatically after the backup, with a short announced downtime |
| Minor | Refused without `--k3s`; one minor at a time |
| More than one minor ahead | Refused even with `--k3s`: upgrade through a GitStack version tested with the intermediate minor |
| Newer than the declared one | Refused: k3s is never downgraded automatically |

While k3s restarts all services stop for a few minutes. If the k3s step
fails, GitStack is not upgraded and there is no automatic rollback of k3s: see
[Rolling back k3s](./operations.md#rolling-back-k3s). With a download mirror
the mirror must also hold the k3s version of the target release.

`gitstack status` shows both versions, and Admin · System warns when a k3s
update is available ([System page](./system.md)).

Details: [Operations: Upgrade](./operations.md#upgrade) and
[Upgrading k3s](./operations.md#upgrading-k3s-rule-n10).
