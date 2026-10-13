---
id: install
title: Installation
---

One command installs k3s, Helm and GitStack on a clean machine that meets the
[requirements](./requirements.md).

```sh
curl -fsSL https://raw.githubusercontent.com/fathorMB/GitStack/main/deploy/install.sh | sudo bash
```

From a checkout of the repository, use `sudo ./deploy/install.sh` instead.

To install a released version (image tag, chart and `gitstack` binary come
from that release, signatures verified):

```sh
curl -fsSL https://raw.githubusercontent.com/fathorMB/GitStack/vX.Y.Z/deploy/install.sh \
  | sudo bash -s -- --version vX.Y.Z --host homehub.local
```

## What the installer does

1. Checks the machine: system, CPU, RAM, disk, free ports, host name.
2. Installs k3s and Helm at the versions pinned by the release, and
   hardens k3s (private kubeconfig, Secrets encrypted at rest).
3. Creates an internal CA and a server certificate (see
   [HTTPS and certificates](./https-and-ca.md)).
4. Verifies the signatures of the binary, the chart and the images
   (see [Verify signatures](./verify-signatures.md)).
5. Deploys the Helm chart and installs the `gitstack` admin command in
   `/usr/local/bin`.
6. Writes `/etc/gitstack/config.yaml` (mode `0600`) and enables the daily
   backup timer.

It is idempotent: running it again without options updates the chart and the
binary and leaves k3s and your data alone. Options you gave before (TLS, host
names, proxy) are remembered.

## The options you will use most

| Option | Purpose |
|---|---|
| `--host NAME\|IP` | Name or address clients use. Repeatable; the first one is the public URL and all go in the certificate. |
| `--tls internal\|letsencrypt` or `--tls-cert/--tls-key` | HTTPS mode. |
| `--postgres-url`, `--postgres-password-file`, `--postgres-ca-file` | Use your own PostgreSQL. |
| `--s3-endpoint`, `--s3-bucket`, `--s3-key-file` | Upload backups to S3. |
| `--http-proxy`, `--registry-mirror`, `--download-mirror`, `--ca-bundle` | Corporate network. |
| `--egress-allow LIST` | Restrict the LAN ranges webhooks and mirrors may reach. |
| `--version vX.Y.Z` | Install a released version. |

The full table, with the environment variables, is in the
[Operations guide](./operations.md#configuration-options).

## First login

The installer prints the public URL, the health-check address and the CA
fingerprint. On first start GitStack creates the `admin` user with a random
password kept in a Kubernetes Secret; you must change it at the first login:

```sh
sudo k3s kubectl -n default get secret gitstack-identity-admin \
  -o jsonpath='{.data.password}' | base64 -d; echo
```

Then open the URL, sign in as `admin`, and trust the internal CA on your
clients ([instructions](./https-and-ca.md#trust-the-internal-ca-on-clients)).
Check the state at any time with `sudo gitstack status`.

## Windows with WSL2 (trial only)

For a trial, a demo or a very small team, `deploy/windows/install-wsl.ps1`
installs an Ubuntu distribution named `GitStack` in WSL2, enables systemd and
runs the same installer. **Do not use it in production**: WSL2 has network and
reliability limits.

Run it in an elevated PowerShell from a checkout:

```powershell
.\deploy\windows\install-wsl.ps1                    # reachable from this PC only
.\deploy\windows\install-wsl.ps1 -ShareOnNetwork    # also from the LAN
.\deploy\windows\install-wsl.ps1 -Uninstall         # remove it
```

- Without options GitStack is reachable at `https://localhost`.
- `-ShareOnNetwork` sets WSL's mirrored networking in `.wslconfig` (needs
  Windows 11 22H2, build 22621 or later) and opens ports 443 and 2222 in the
  Windows firewall.
- A scheduled task starts the distribution at login.
