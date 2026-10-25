---
id: ci
title: CI, workflows and runs
---

GitStack runs workflows written in the GitHub Actions syntax. A workflow lives
in `.gitstack/workflows/` (or in `.github/workflows/`, which is read too; when
a file name exists in both folders, `.gitstack/workflows/` wins). Each job runs
in a throw-away pod of the built-in runner, or on an external runner chosen
with `runs-on:`. The CI is **off by default**: an administrator switches it on
(see the Admin guide, "Built-in CI").

## What starts a run

| Event | Notes |
|---|---|
| `push` | branches, tags and `paths` filters; never for archived repositories, and off by default for pull mirrors |
| `pull_request` | pull requests of the same repository |
| `workflow_dispatch` | by hand, with `write` on the repository; `inputs:` are validated |
| `schedule` | at most every 15 minutes; paused after 60 days without a push |

A commit message containing `[skip ci]` starts nothing. A new push cancels the
run still queued for the same branch or pull request. Pushes made with a job
token never start other workflows.

## From the terminal

```sh
gs workflow list -R alice/app
gs workflow run build.yml -R alice/app --ref main -f env=prod   # prints the run id
gs run watch <run-id> -R alice/app --exit-status                 # waits; exit 1 if the run does not succeed
gs run view <run-id> -R alice/app --log-failed                   # logs of the failed jobs only
gs run list -R alice/app --json id,status,conclusion
gs run rerun <run-id> -R alice/app
gs run cancel <run-id> -R alice/app --yes
```

`--json` works on every command. A required `inputs:` entry missing from `-f`
makes `gs workflow run` fail without starting anything. Agents can wait for the
CI with `gs run watch --exit-status` and read the failure with
`--log-failed` before fixing it.

## Secrets, variables and the job token

Secrets and variables belong to a repository or to an organization. They are
stored encrypted, never shown again and masked in the logs. Every job gets a
short-lived token for its own repository only. It never has more permissions
than the person who started the run; a `permissions:` block can only reduce it.

## Logs, artifacts and caches

- **Logs** are streamed while the job runs and kept for 90 days.
- **Artifacts** are kept for 30 days (up to 2 GB each).
- **Caches** are up to 10 GB per repository; the least used go first, and the
  ones not used for 7 days are removed. All values can be changed by an
  administrator.

`actions/cache` works: the first run prints `Cache saved with key: …` and the
next run with the same key prints `Cache restored from key: …`.

`actions/upload-artifact@v4` and `download-artifact@v4` talk to a protocol that
GitStack does not implement. The step fails with HTTP 501 and a message that
says so, and points to the alternative: upload the ZIP with
`PUT /ci/v1/artifacts/{name}` of the CI gateway using the job token, or use an
external runner with a compatible artifact service.

## Results

Each job reports a commit status named `workflow / job`. You can require it
before merging (see [Pull requests](./pull-requests.md)). The Actions tab of a
repository lists runs with a live log, "Re-run", "Cancel" and a manual start.
The person who started a run is notified when it fails and when it goes green
again; the webhook events `workflow_run` and `workflow_job` are sent as well.

## Limits to know

Jobs run without privileges and without access to the internal network of the
cluster. `services:`, `docker build`, the `gh` CLI and `runs-on: windows-latest`
do not work in the built-in runner pods: use an external runner, or the
registry and release features of GitStack instead. By default 1 job (minimum
profile) or 3 jobs (recommended profile) run at the same time, with 2 vCPU,
4 GB of memory, 20 GB of disk and 2 hours each; GitStack's own services and
hosted apps come before the jobs.
