# Operations Guide — GitStack

> **Scope:** This guide covers installation, backup, restore, upgrade, push
> mirrors, and diagnostics of a GitStack instance deployed on a single Linux host with k3s.
> It is written for operators who manage the machine directly (for example the
> `homehub` appliance described in M-08 [c_8458909a21d9035f]).

## Installation

### Prerequisites

GitStack runs on a clean Ubuntu Server 24.04 LTS x86_64 machine.
Other distributions and WSL2 arrive with M-08.

| Profile | CPU | RAM | Disk |
|---|---|---|---|
| **Minimum** (blocking) | 4 vCPU | 8 GB | 60 GB (≥ 20 GB free on the filesystem that holds `/var/lib/rancher`) |
| **Recommended** (advisory) | 8 vCPU | 16 GB | 200 GB SSD (≈ 100 users) |

All values use decimal GB (1 GB = 10^9 bytes). The installer checks both the
total filesystem size and the free space separately; free space drops after
k3s and its images are pulled, so the preflight runs on every re-execution too.

Ports required on the host:

| Port | Purpose |
|---|---|
| 22 | SSH to the host (unchanged) |
| 443 | HTTPS (GitStack web UI, API, Git HTTPS) |
| 2222 | Git SSH (the `git` Service inside the cluster; never the host's port 22) |
| 80 | Redirected to 443 by Traefik (except `/downloads/ca.crt`) |

The installer also needs outbound HTTPS to GitHub, Docker Hub, and the k3s/Helm
download mirrors.

Disk layout recommendation:

- **Data disk** (SSD) mounted at `/var/lib/rancher` for k3s storage, Postgres,
  and the Git repositories. This is where the bulk of the data lives.
- **Backup disk** (separate physical disk if possible) for the backup archive.
  See the [Backup](#backup) section below.

### The installer

```sh
sudo ./deploy/install.sh
```

Or, in one command:

```sh
curl -fsSL https://raw.githubusercontent.com/fathorMB/GitStack/main/deploy/install.sh | sudo bash
```

The installer:

1. Checks OS, CPU, RAM, disk, port availability, and hostname resolution
   (skip with `--skip-preflight` only if you have already verified).
2. Installs k3s (`v1.36.4+k3s1`, pinned) and Helm (`v3.16.3`, pinned).
3. Generates an internal CA and a server certificate (see [HTTPS](#https)).
4. Pulls GitStack images and deploys the Helm chart (`deploy/gitstack`) with
   `helm upgrade --install`.
5. Installs the `gitstack` admin binary in `/usr/local/bin/gitstack`.
6. Writes the configuration to `/etc/gitstack/config.yaml` (mode `0600`).
7. Enables a daily backup timer (see [Backup](#backup)).

The script is idempotent: re-running it without options upgrades the chart and
the admin binary if they differ, without touching k3s or the data.

### Configuration options

| Option | Environment variable | Default | Description |
|---|---|---|---|
| `--force` | `GITSTACK_FORCE=1` | off | Proceed on an unsupported OS (warning, no guarantee). Does **not** skip hardware checks. |
| `--skip-preflight` | `GITSTACK_SKIP_PREFLIGHT=1` | off | Skip OS, CPU, RAM, disk, port, and hostname checks. |
| `--release-name NAME` | `GITSTACK_RELEASE_NAME` | `gitstack` | Helm release name. |
| `--namespace NS` | `GITSTACK_NAMESPACE` | `default` | Kubernetes namespace. |
| `--chart-dir PATH` | `GITSTACK_CHART_DIR` | (detected) | Use a local chart copy instead of downloading from the repository. |
| `--image-tag TAG` | `GITSTACK_IMAGE_TAG` | (auto-resolved) | Image tag for gateway/identity/core/web. |
| `--image-registry HOST` | `GITSTACK_IMAGE_REGISTRY` | (from chart) | Container registry for GitStack images. Useful for a mirror. |
| `--admin-binary PATH\|URL` | `GITSTACK_ADMIN_BINARY` | (download from release) | Path or URL of the `gitstack` admin binary; its SHA-256 is verified. |
| `--admin-sha256 HEX` | `GITSTACK_ADMIN_SHA256` | (from `<binary>.sha256`) | Expected SHA-256 of the admin binary. |
| `--host NAME\|IP` | — | (auto-detected) | Hostname or IP that clients use to reach GitStack. Repeatable — all values become SANs in the certificate. The first is the public URL. The installer resolves the name and verifies it points to a local address. |
| `--tls internal` | `GITSTACK_TLS_MODE=internal` | `internal` | Internal CA (default). The installer generates a CA whose key stays on the host only. |
| `--tls letsencrypt` | `GITSTACK_TLS_MODE=letsencrypt` | — | Public certificate from Let's Encrypt (HTTP-01 challenge). Requires `--host` pointing to a DNS name reachable on the internet on port 80. |
| `--tls-cert FILE --tls-key FILE` | `GITSTACK_TLS_CERT`, `GITSTACK_TLS_KEY` | — | Client-supplied PEM certificate and key. |
| `--tls-email EMAIL` | `GITSTACK_TLS_EMAIL` | — | Email for Let's Encrypt (recommended). |
| `--insecure-http` | `GITSTACK_TLS_MODE=insecure` | — | HTTP only, no TLS. **Only for local tests**; the web UI shows a warning. |
| `--values FILE` | — | — | Additional Helm values file (`-f`). Repeatable. |
| `--set KEY=VALUE` | — | — | Additional Helm value (`--set`). Repeatable. |

Other environment variables: `INSTALL_K3S_VERSION`,
`GITSTACK_BACKUP_DIR`, `GITSTACK_BACKUP_RETENTION`,
`GITSTACK_BACKUP_TIMER_HOUR` (0–23, default `02`).

### HTTPS modes

| Mode | Certificate | Key location |
|---|---|---|
| Internal (default) | Generated by the installer | `/etc/gitstack/tls/` on the host only |
| Let's Encrypt | Public, obtained and renewed by Traefik (HTTP-01) | Managed by Traefik inside the cluster |
| Client certificate | Your own PEM | `/etc/gitstack/tls/` |
| HTTP only | None | — |

In all TLS modes Traefik serves port 443 and redirects port 80 to 443
(308 permanent). The only exception is `/downloads/ca.crt` (served over HTTP
even in TLS mode because you need it to trust HTTPS the first time).

After installation, the summary printed by the installer shows the public URL,
the health-check endpoint, how to fetch the admin password, and — for internal
CA — how to download and trust the CA on your client machines.

#### Trusting the internal CA on clients

1. Download the certificate: `http://<host>/downloads/ca.crt` (or
   `https://` with `-k` for this download only, or copy from
   `/etc/gitstack/tls/ca.crt` on the server).
2. **Verify the SHA-256 fingerprint** against the one printed by the installer
   (or `sudo gitstack-tls fingerprint`): without this check a man-in-the-middle
   at the first request could hand you a fake CA.

   ```sh
   openssl x509 -in ca.crt -noout -fingerprint -sha256
   ```

   - **Linux (Debian/Ubuntu):**
     ```sh
     sudo cp ca.crt /usr/local/share/ca-certificates/gitstack-ca.crt
     sudo update-ca-certificates
     ```
     Fedora/RHEL: copy to `/etc/pki/ca-trust/source/anchors/` and run
     `sudo update-ca-trust`. Firefox uses its own store. Chrome/Chromium on
     Linux uses NSS: `certutil -d sql:$HOME/.pki/nssdb -A -t "C,," -n gitstack
     -i ca.crt`.

   - **macOS:**
     ```sh
     sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ca.crt
     ```

   - **Windows (PowerShell as Administrator):**
     ```powershell
     Import-Certificate -FilePath .\ca.crt -CertStoreLocation Cert:\LocalMachine\Root
     ```

   - **Git only (system-wide):**
     ```sh
     git config --global http.sslCAInfo /path/to/ca.crt
     ```

   - **curl:** `curl --cacert ca.crt https://<host>/api/healthz`

### Admin password

On first startup `identity` creates the `admin` user with a randomly generated
password stored in a Kubernetes Secret (`<release>-identity-admin`). It is not
printed by the installer. To read it:

```sh
KUBECONFIG=/etc/rancher/k3s/k3s.yaml k3s kubectl -n default get secret gitstack-identity-admin -o jsonpath='{.data.password}' | base64 -d; echo
```

On first login the admin must change this password.

---

## Backup

GitStack provides a coherent backup command that takes a short read-only
window (scales down `gateway` and `git` to zero replicas while the backup
runs, then restores them).

### Where backups go

Backups are written to the directory configured in
`/etc/gitstack/config.yaml` under `backup.destination`. The default is
`/var/backups/gitstack`.

The `gitstack` admin binary is installed at `/usr/local/bin/gitstack`.

### Creating a backup

```sh
sudo gitstack backup
```

This creates an archive in the configured destination directory with a name
like `gitstack-backup-20261006T143000Z-sha-f4f3a2b.tar.gz`.

**With encryption (recommended for off-host storage):**

```sh
sudo gitstack backup --key-file chiave.txt
```

The archive is encrypted with AES-256-GCM. You will need the same key file to
restore it. If you omit `--key-file` the archive is unencrypted (mode `0600`,
root-only).

**Override destination and retention for a single run:**

```sh
sudo gitstack backup --dest /mnt/backup/gitstack --retention 14
```

### What the archive contains

| File inside archive | Contents |
|---|---|
| `manifest.json` | Format version, server version, commit SHA, binary version, timestamp, list of files with sizes and SHA-256 checksums |
| `database.sql` | `pg_dump` of the `identity` and `core` schemas |
| `git-data.tar` | All Git repositories (the `git-data` volume) |
| `attachments.tar` | Issue attachments (only if the directory exists) |
| `secrets.json` | Kubernetes secrets used by GitStack (certificate data, SSH host keys, OIDC keys, etc.) |
| `config/` | Copy of the contents of `/etc/gitstack/` (CA certificate, CA key, configuration files) |

A `gitstack-backup-*.sha256` sidecar file is written next to each archive.

### Retention

By default the installer sets `backup.retention` to **7** in the config file.
The command keeps the newest N archives and removes the oldest; the sidecar
`.sha256` files are removed together.

You can override retention for a single run with `--retention`.

### Enabling the daily backup timer

The installer enables a systemd timer (`gitstack-backup.timer`) that runs
`sudo gitstack backup` every day. The hour is configurable with the environment
variable `GITSTACK_BACKUP_TIMER_HOUR` (default `02`):

```sh
# Check the next scheduled run:
systemctl list-timers gitstack-backup.timer
```

The timer is configured in `/etc/systemd/system/gitstack-backup.timer` with
`OnCalendar=*-*-* 02:00:00`. To change the hour, re-run the installer with
the new value:

```sh
sudo GITSTACK_BACKUP_TIMER_HOUR=03 ./deploy/install.sh
```

Do not edit the timer unit manually: the installer overwrites it on re-run.

To disable the timer:

```sh
sudo systemctl disable --now gitstack-backup.timer
```

### Copying backups off the machine

Backups are regular files in the configured destination directory. Use `rsync`
to copy them to a separate disk or remote host:

**To a backup disk mounted at `/mnt/backup` (on the server itself):**

```sh
sudo rsync -a /var/backups/gitstack/ /mnt/backup/gitstack/
```

**To a remote host (initiated from the server):**

```sh
rsync -avz /var/backups/gitstack/gitstack-backup-* \
  gitstack@backup-server:/backups/gitstack/
```

**From a remote host to the server (pull):**

```sh
rsync -avz gitstack@homehub:/var/backups/gitstack/gitstack-backup-* \
  ./backups/
```

> **Keep the encryption key separate from the archive.** Store the key on a
> different disk or off-site; never put it in the backup destination folder.
> An encrypted archive without the key is unrecoverable.
---

## Restore

Restores a backup archive onto a GitStack installation. The archive must
have been produced by `gitstack backup` from the **same server version**:
the restore refuses the archive if the version tag inside does not match the
installed version. The command overwrites the database, all Git repositories,
the attachments directory, and the Secret data, then restarts the affected
services.

### Steps

1. Make sure the instance is running and the server version matches the
   archive version: the restore replaces database tables, Git repositories,
   attachments, and Secret data in place. If the cluster is down (for
   example k3s is stopped) the command exits with code `4` (cluster
   unreachable).

2. Run the restore command pointing at the archive:

   ```sh
   sudo gitstack restore /var/backups/gitstack/gitstack-backup-20261006T154627Z-sha-20636805d1756798054dd0b7c570ae0a3d4cc6ee.tar.gz
   ```

   **With an encrypted archive:**

   ```sh
   sudo gitstack restore --key-file /root/gs.key \
     /var/backups/gitstack/gitstack-backup-20261006T154627Z-sha-20636805d1756798054dd0b7c570ae0a3d4cc6ee.tar.gz.enc
   ```

   **Override the backup destination directory** (where the archive lives)
   from outside the config path:

   ```sh
   sudo gitstack restore --dest /mnt/backup/gitstack \
     gitstack-backup-20261006T154627Z-sha-20636805d1756798054dd0b7c570ae0a3d4cc6ee.tar.gz
   ```

3. Wait for the command to finish. On success it prints:

   ```
   archivio verificato: versione sha-... del 2026-10-06T15:46:27Z, 12 file
   fermo i servizi: core, gateway, git, identity
   ripristino del database (in una sola transazione)
   ripristino dei repo (git-data)
   ripristino degli allegati
   ripristino dei Secret
   ripristinati 7 file di configurazione (config.yaml resta quello della nuova installazione)
   riavvio i servizi
   Restore completato: database, repo, allegati, Secret e configurazione ripristinati.
   ```

4. Verify with `gitstack status` that all services are ready (exit code `0`).

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Restore completed successfully |
| `2` | Usage error: missing archive or invalid option |
| `3` | Configuration file missing or unreadable |
| `4` | Cannot reach the Kubernetes cluster |
| `5` | Requires root (re-run with `sudo`) |
| `6` | Restore refused — version mismatch, corrupted archive, or encrypted archive without `--key-file` |
| `70` | Unexpected error |

---

## Upgrade

`gitstack upgrade` updates the GitStack Helm chart and the admin binary.
It performs a **preventive backup** before touching anything, applies the
migrations of the target chart, verifies the health of the services, and
**rolls back automatically** if the post-upgrade health check fails.
### Dry-run (recommended before any real upgrade)

```sh
sudo gitstack upgrade --dry-run
```

Runs only the checks — target version, no downgrade, current health,
binary with checksum, chart, and images — without modifying anything.
On success, exit `0`, summary includes the target version and
image count.

When the target commit has no published release yet:

```
ERRORE: upgrade rifiutato: checksum del binario non disponibile: https://github.com/fathorMB/GitStack/releases/download/sha-fc0a16ecc4018ea81a4f71f7d96089668cf3a8b0/gitstack-linux-amd64.sha256: HTTP 404
Niente è stato modificato.
```

Exit code `6` (`ExitRefused`), nothing changed.

### Full upgrade

```sh
sudo gitstack upgrade
```

Steps executed:

1. **Checks** (same as dry-run): target version, no downgrade, current
   health, binary with checksum, chart and images exist. If any check
   fails, exit `6` (`ExitRefused`), nothing changed.
2. **Preventive backup** of the current state (same options as
   `gitstack backup`).
3. **Helm upgrade** of the chart (migrations run when services start),
   then wait for all services to become healthy (default timeout 10 min;
   override with `--timeout`).
4. **Local substitution**: replace the admin binary, the local copy of
   the chart, and `image_tag` in the config — in the order they can be
   undone.

**Rollback** (triggered on step 3 failure):

1. Restore the previous binary and chart copy.
2. Run `helm rollback` to the previous release revision.
3. If the migrations touched the database (schema versions changed),
   run `gitstack restore` with the preventive backup just created.

On rollback success, exit `7` (`ExitRolledBack`); on rollback failure,
exit `8` (`ExitBroken`) with manual intervention instructions.

### Options

```sh
sudo gitstack upgrade --to v0.2.0 --dest /mnt/backup/gitstack \
  --key-file /root/gs.key --timeout 10m \
  --set service.gateway.replicas=2 --values extra-values.yaml
```

| Flag | Purpose | Default |
|---|---|---|
| `--to TAG\|SHA` | Target version: tag or commit SHA | latest on `main` |
| `--dest DIR` | Override backup destination directory | `backup.destination` from config |
| `--key-file FILE` | Path to AES-256-GCM encryption key | none (unencrypted backup) |
| `--timeout D` | Max time for service health checks | `10m` |
| `--set KEY=VAL` | Helm value override | — (repeatable) |
| `--values FILE` | Additional Helm values file | — (repeatable) |

Environment variables: `GITSTACK_REPO` (default `fathorMB/GitStack`),
`GITSTACK_REF` (default `main`).

### What happens if the upgrade fails

| Exit code | Outcome |
|---|---|
| `0` | Upgrade completed successfully |
| `2` | Usage error (wrong option, missing argument, too-short timeout) |
| `4` | Cannot reach the Kubernetes cluster |
| `6` | Upgrade refused (incompatible target, downgrade, health check, missing binary checksum) — nothing changed |
| `7` | **Rolled back** — the upgrade failed and the previous state was restored automatically |
| `8` | **Broken** — the upgrade failed and the rollback also failed; manual intervention required |
| `70` | Unexpected error (backup, network, or other unrecoverable failure) |

---

## Mirror

A repository can be mirrored to another Git server over HTTPS (GitHub, or a second GitStack). On every push to the **default branch** or to a **tag**, and whenever you press **Sync now**, GitStack pushes the default branch and all tags to the remote. Other branches are not mirrored.

A mirror **never forces**: it never overwrites a branch or a tag on the remote and never deletes a ref there. If the remote has a different history, the mirror stops (see [If a mirror stops: divergence](#if-a-mirror-stops-divergence)).

Managing mirrors needs the `admin` role on the repository. You find them under the repository's **Settings → Mirrors** (**Add mirror**), or through the API (`/api/v1/repos/{owner}/{repo}/mirrors`). The token is stored encrypted and is never shown again.

### Mirroring to GitHub

1. On GitHub, create an **empty** repository (no README, no licence, no `.gitignore`). A repository that already has commits has a different history, and the first push would stop as divergent.
2. Create a **fine-grained personal access token** (GitHub → Settings → Developer settings → Personal access tokens → Fine-grained tokens):
   - *Repository access*: **Only select repositories**, and pick just that repository.
   - *Repository permissions*: **Contents: Read and write**. Nothing else — GitHub adds *Metadata: Read-only* by itself. No account permissions.
   - Set an expiration date and note it down: when the token expires, the mirror goes to `error` and stays there until you replace the token.
3. In GitStack, open the repository's **Settings → Mirrors → Add mirror** and fill in:
   - *URL*: `https://github.com/OWNER/REPO.git` (no credentials, no query).
   - *Username*: your GitHub user name.
   - *Token*: the token from step 2.

The same through the API (the token is only ever sent in this request):

```bash
curl -fsS -X POST "https://gitstack.example.com/api/v1/repos/alice/app/mirrors" \
  -H "Authorization: Bearer $GITSTACK_TOKEN" -H "Content-Type: application/json" \
  -d '{"url":"https://github.com/alice/app.git","username":"alice","token":"'"$GITHUB_TOKEN"'"}'
```

The first push starts right away. The mirror is healthy when its state is **In sync**; `GET .../mirrors/{id}/runs` (and the run log in the UI) lists every attempt with the result for each ref.

Notes:

- If the default branch is **protected** on GitHub, GitHub may refuse the push (`protected branch hook declined`). That is a refusal, not a divergence: the mirror goes to `error` and retries. Allow the push on that branch, or do not protect it.
- Creating a mirror answers `503 mirror_secrets_unavailable` if the installation has no secret encryption key (`GITSTACK_WEBHOOK_SECRET_KEY`, the same one used for webhook secrets).

### States and retries

| State | Meaning |
|---|---|
| `pending` | A push is queued (never pushed yet, or something new to push). |
| `syncing` | A push is running. |
| `in_sync` | The remote has what the last successful push sent. |
| `error` | The last attempt failed (network, credential, refusal by the remote…). It retries after 30 s, 2 min, 10 min, 1 h, 3 h, then every 6 h. |
| `diverged` | The remote has a different history. **Stopped**: no automatic retries. |

The last error is shown in the UI and as `lastError` in the API, without any credential.

### Destinations on a private network or with a private CA

- **Egress policy.** Like webhooks, mirror destinations go through the egress protection (`egress.*` in the chart values). Loopback, link-local and the cluster networks are always blocked. A server on your LAN is allowed by default; if you set `egress.allow`, only what it lists is allowed, so add the destination's address (an IP/CIDR, or a host name). A blocked destination ends in a `blocked` run and is not retried.
- **Private CA.** Git trusts the system CAs. For a destination with a certificate from a private CA (for example a second GitStack with its internal CA) put the CA in a ConfigMap and point the chart at it:

  ```bash
  kubectl create configmap mirror-ca --from-file=ca.crt=/path/to/ca.crt
  helm upgrade gitstack <chart> --reuse-values --set git.mirror.caConfigMap=mirror-ca
  ```

  The file **replaces** the system CAs for mirror pushes only. If the same installation also mirrors to GitHub, the file must contain the public CAs as well as yours (for example `cat /etc/ssl/certs/ca-certificates.crt your-ca.crt`).

### If a mirror stops: divergence

A mirror becomes **Diverged** when the remote cannot take the push without overwriting something: the remote branch has commits GitStack does not have (someone pushed there), or a tag with the same name already points to a different commit. GitStack stops, leaves the remote untouched, and notifies the repository's admins (notification reason `mirror`).

To resolve it, first decide which side is right, look at the run log to see which refs were refused, then:

- **The remote has commits you want to keep.** Bring them into GitStack, then sync:

  ```bash
  git clone https://gitstack.example.com/alice/app.git && cd app
  git remote add mirror https://github.com/alice/app.git
  git fetch mirror
  git merge mirror/main        # or rebase your work on it
  git push origin main
  ```

  GitStack now contains the remote's commits, so the next push is a plain fast-forward.
- **The remote is wrong** (an accidental commit, a test tag). Remove it on the remote — reset the branch there, or delete the tag (`git push mirror :refs/tags/v1.0`). The mirror never does this for you.
- **A tag points to the wrong commit on one side.** Delete the tag on the side that is wrong and recreate it where it should be.

Then press **Sync now** (or `POST /api/v1/repos/{owner}/{repo}/mirrors/{id}/sync`, which answers `202`). The mirror pushes again and, if nothing else is different, goes back to **In sync**. A mirror stays `diverged` until you do: new pushes to GitStack do not restart it.

---

## Diagnostics

### `gitstack status`

Reports the server version, the admin binary version, the host, the Helm
release, the readiness of every service, the API reachability, the last
backup timestamp, and an overall health line.

```sh
sudo gitstack status
```

Example output:

```
GitStack
  Versione server: sha-20636805d1756798054dd0b7c570ae0a3d4cc6ee
  Versione gitstack: sha-20636805d1756798054dd0b7c570ae0a3d4cc6ee
  Host: 172.28.5.19 (SSH git: porta 2222)
  Release: gitstack (namespace default)

Servizi
  gitstack-core                1/1 pronti  OK
  gitstack-gateway             1/1 pronti  OK
  gitstack-git                 1/1 pronti  OK
  gitstack-identity            1/1 pronti  OK
  gitstack-nats                1/1 pronti  OK
  gitstack-postgres            1/1 pronti  OK
  gitstack-web                 1/1 pronti  OK
  api (gateway /healthz)         OK (https://172.28.5.19/api/healthz: HTTP 200)

Ultimo backup: ultimo successo: 2026-10-06 15:46:27 (/var/backups/gitstack/gitstack-backup-20261006T154627Z-sha-20636805d1756798054dd0b7c570ae0a3d4cc6ee.tar.gz.enc)

Stato: sano
```

**JSON mode** (`--json`): produces structured output for scripting:

```sh
sudo gitstack status --json
```

The command reads `/etc/gitstack/config.yaml` (mode `0600`); run it
under `sudo` or with `GITSTACK_CONFIG` pointing to an accessible file.

Exit codes: `0` (all healthy), `1` (at least one service unhealthy),
`4` (cannot query the cluster), `3` (configuration error).

### `gitstack version`

Prints only the admin binary version, without any config read. Does not
require `sudo`:

```sh
gitstack version
```

Output:

```
gitstack sha-20636805d1756798054dd0b7c570ae0a3d4cc6ee
```

### TLS diagnostics

The script `deploy/gitstack/tls/gitstack-tls.sh` (installed as
`/usr/local/bin/gitstack-tls`) supports:

- `gitstack-tls fingerprint` — prints the CA certificate SHA-256 fingerprint
- `gitstack-tls renew` — regenerates the server certificate (internal CA mode)

Both need `sudo` on internal CA because they read/write under
`/etc/gitstack/`.

---

## Quick reference

### Commands

| Command | Needs `sudo`? | Purpose |
|---|---|---|
| `gitstack version` | no | Print the admin binary version |
| `gitstack status [--json]` | yes* | Health report: services, API, last backup |
| `gitstack backup` | yes | Create a coherent backup archive |
| `gitstack backup --key-file KEYFILE` | yes | Same, encrypted with AES-256-GCM |
| `gitstack restore ARCHIVE` | yes | Restore an archive (same version only) |
| `gitstack restore --key-file KEYFILE ARCHIVE` | yes | Restore an encrypted archive |
| `gitstack upgrade` | yes | Upgrade with preventive backup and automatic rollback |
| `gitstack upgrade --dry-run` | yes | Check readiness without making changes |
| `gitstack config set host NEWNAME` | yes | Change the host name (certificates, services, config) |

\* `gitstack status` reads `/etc/gitstack/config.yaml` (mode `0600`),
so it fails as a non-root user with a permission error.

### Common flags

| Flag | Applies to | Purpose |
|---|---|---|
| `--config PATH` | All commands | Override config file path (default: `/etc/gitstack/config.yaml`) |
| `--dest DIR` | `backup`, `restore`, `upgrade` | Override the backup destination directory |
| `--key-file FILE` | `backup`, `restore`, `upgrade` | Path to the AES-256-GCM encryption key |
| `--to TAG\|SHA` | `upgrade` | Target version; default is latest on the `main` branch |
| `--timeout D` | `upgrade` | Max time for health checks (default `10m`) |
| `--set KEY=VAL` | `upgrade` | Helm value override, repeatable |
| `--values FILE` | `upgrade` | Additional Helm values file, repeatable |
| `--json` | `status` | Output in JSON format |
| `--dry-run` | `upgrade` | Check readiness without making changes |

### Exit codes summary

| Code | Meaning |
|---|---|
| `0` | Success (`status` = healthy, or command completed) |
| `1` | `status` reports at least one unhealthy service |
| `2` | Usage error (wrong command, missing argument) |
| `3` | Configuration file missing or unreadable |
| `4` | Cannot reach the Kubernetes cluster |
| `5` | Command requires root (re-run with `sudo`) |
| `6` | Refused: incompatible version, corrupted archive, or missing key |
| `7` | Upgrade rolled back automatically |
| `8` | Upgrade failed and rollback also failed — manual intervention needed |
| `70` | Unexpected error (backup failure, network, unrecoverable) |

### Configuration files

| Path | Purpose |
|---|---|
| `/etc/gitstack/config.yaml` | Main configuration (mode `0600`) |
| `/etc/gitstack/tls/` | CA certificate, CA key, server certificate (internal CA mode) |
| `/etc/gitstack/backup-state.json` | Last backup status written by `gitstack backup` |

---

*Guida operativa — install.sh (T-09 di M-08 [c_8458909a21d9035f]),
decisioni D17–D19 [c_1d1d61aca3dea601]*
