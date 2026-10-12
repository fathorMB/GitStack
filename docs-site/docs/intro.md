---
id: intro
title: GitStack documentation
slug: /
sidebar_label: Quick install
---

GitStack is a personal, on-prem "mini GitHub": remote Git repositories and
issues, managed from a web UI, a command-line tool (`gs`) or coding agents,
installed on a single Linux machine with one command.

This site has four sections:

| Section | For | Start here |
|---|---|---|
| [User](./user/index.md) | People and agents who use an instance | [Getting started](./user/getting-started.md) |
| [Admin](./admin/index.md) | Whoever installs and runs an instance | [Operations guide](./admin/operations.md) |
| [API](./api/index.md) | Integrations | [API reference](./api/index.md) |
| [CLI](./cli/index.md) | People and agents using `gs` | [`gs` reference](./cli/index.md) |

## Quick install (Ubuntu)

Requirements: Ubuntu Server 22.04 or 24.04 LTS (x86_64), at least 4 vCPU,
8 GB RAM and 60 GB of disk, with outbound HTTPS to GitHub, Docker Hub and the
k3s/Helm download mirrors. The full list of requirements and ports is in the
[Operations guide](./admin/operations.md#prerequisites).

```sh
curl -fsSL https://raw.githubusercontent.com/fathorMB/GitStack/main/deploy/install.sh | sudo bash
```

The installer checks the machine, installs k3s and Helm, creates an internal
CA and a certificate, deploys GitStack and installs the `gitstack` admin
binary. It is idempotent: running it again upgrades the chart and the binary
without touching k3s or the data. All options (HTTPS modes, external
PostgreSQL, proxies, a specific release with `--version`) are in the
[Operations guide](./admin/operations.md#the-installer).

## Before you go to production

- [Security policy](./admin/security-policy.md): supported versions, how to
  report a vulnerability privately, response times.
- [Versioning policy](./admin/versioning.md): Semantic Versioning, release
  cadence and supported upgrade paths.
