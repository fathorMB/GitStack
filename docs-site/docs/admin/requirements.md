---
id: requirements
title: Requirements and supported systems
---

GitStack runs on **one Linux machine** with k3s (a single-node Kubernetes).
The installer sets everything up; you do not need to know Kubernetes.

## Supported systems

Only Ubuntu Server 22.04 and 24.04 are officially supported in v1.0 (N2, revised 2026-10-18).

| System | Status |
|---|---|
| Ubuntu Server 22.04 LTS and 24.04 LTS (amd64) | Officially supported in v1.0. Install, backup, restore and upgrade run in CI on both. |
| Pop!_OS 22.04 and 24.04 (amd64) | Not officially supported in v1.0: `install.sh` proceeds without `--force` but warns in the preflight, with no guarantee. CI has no Pop!_OS runner; the real run stays on the test VM. |
| Windows with WSL2 | **Trial and demo only**, never production. See [WSL2 on Windows](./install.md#windows-with-wsl2-trial-only). |
| Other Linux distributions | Not supported. `install.sh --force` proceeds with a warning and no guarantee. |

## Hardware

| Profile | CPU | RAM | Disk |
|---|---|---|---|
| Minimum (the installer refuses less) | 4 vCPU | 8 GB | 60 GB, with at least 20 GB free where `/var/lib/rancher` lives |
| Recommended (a warning only) | 8 vCPU | 16 GB | 200 GB SSD, for about 100 users |

Sizes are in decimal GB. The installer checks the total size and the free
space separately.

Keep the data disk (k3s storage, PostgreSQL, Git repositories) on an SSD, and
the backups on a different physical disk if you can.

## Network

| Port | Purpose |
|---|---|
| 443 | HTTPS: web UI, API and Git over HTTPS |
| 80 | Redirected to 443, except `/downloads/ca.crt` |
| 2222 | Git over SSH (the host's own SSH on port 22 is never touched) |

The installer needs outbound HTTPS to GitHub, Docker Hub and the k3s and Helm
download sites. On a network without direct access use a proxy and internal
mirrors: see [Corporate network](./corporate-network.md).

## Optional pieces

- An existing PostgreSQL 15, 16 or 17: [External PostgreSQL](./external-postgres.md).
- An S3-compatible bucket for off-host backups: [Backup and restore](./backup-restore.md#backup-to-s3).
- A DNS name reachable from the internet, only for Let's Encrypt:
  [HTTPS and certificates](./https-and-ca.md).

The full reference is the [Operations guide](./operations.md#prerequisites).
