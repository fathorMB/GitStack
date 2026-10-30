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

## Building and publishing an image

A job with `runs-on: gitstack-build` runs in a pod of the build namespace, with
rootless BuildKit next to the runner: no Docker, no daemon, no privileges on the
node. An administrator switches it on (`ci.build.enabled`, or `--ci-build` of the installer);
with it off, such a job fails without starting. The command `gitstack-build`
builds a `Dockerfile` and pushes the image to the registry of the repository with
the token of the job:

```yaml
name: image
on: push
permissions:
  contents: read
  packages: write
jobs:
  image:
    runs-on: gitstack-build
    steps:
      - run: gitstack-build --tag ${{ github.sha }}
```

`gitstack-build --tag <tag> [--name <sub-name>] [--file Dockerfile] [--build-arg K=V] [context]`
publishes `<registry>/<owner>/<repo>[/<sub-name>]:<tag>`. The image is always
the one of the repository running the job, because its token is valid for no
other. The token needs `packages: write`: the ceiling of the repository is `read`
until a repository admin raises it (`PUT /repos/{owner}/{repo}/actions/permissions`
with `"packages": "write"`), and the token never exceeds the person who started
the run. The image shows up in the Packages tab with "ci job token"
as the pusher; the instance signs it as it does for every image from the CI.

The build pod has the same network barriers as any job: it reaches the CI
gateway, the registry and the public Internet, never the internal network, the
database or the other services. Base images come from the Internet (for example
Docker Hub); a mirror of the actions on a private network
(`ci.runner.actionMirror`) is not reachable from this pod.

## What the job image contains

The built-in runner image is Debian-based and has: Node.js (needed by
JavaScript actions), `git`, `curl`, `ca-certificates`, `act`, `buildctl`, and a
basic native toolchain: `gcc` (also as `cc`), `ld` and the C library headers
(`libc6-dev`), `make`, `jq`, `xz` and `unzip`. A C program compiles, links and
runs, and so does a Rust crate with proc-macros once Rust is installed.

It does **not** contain:

- language toolchains (Rust, Go, Python, `cmake` and the like);
- project libraries (for example WebKitGTK or other `-dev` packages);
- `docker`, `sudo` or `apt`: the job runs as an unprivileged user with a
  read-only root filesystem, and only `/work` (the home directory) and `/tmp`
  are writable.

Install languages and libraries from the workflow, without root, under `/work`.
For example, `rustup` with `CARGO_HOME` and `RUSTUP_HOME` in `/work`, or a
prebuilt tarball unpacked there. Software that needs system packages other
than the ones above cannot be installed in this runner: do not assume that
every native project builds here. Use an external runner for that.

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
registry and release features of GitStack instead. To build an image use
`runs-on: gitstack-build` (see above) instead of `docker build`. By default 1 job (minimum
profile) or 3 jobs (recommended profile) run at the same time, with 2 vCPU,
4 GB of memory, 20 GB of disk and 2 hours each; GitStack's own services and
hosted apps come before the jobs.
