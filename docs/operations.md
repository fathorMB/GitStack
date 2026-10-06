# Operations Guide — GitStack

> **Scope:** This guide covers installation, backup, restore, upgrade, and
> diagnostics of a GitStack instance deployed on a single Linux host with k3s.
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
| `--host NAME\|IP` | `GITSTACK_TLS_MODE` | (auto-detected) | Hostname or IP that clients use to reach GitStack. Repeatable — all values become SANs in the certificate. The first is the public URL. The installer resolves the name and verifies it points to a local address. |
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
`gitstack backup` every day. The hour is configurable with the environment
variable `GITSTACK_BACKUP_TIMER_HOUR` (default `02`):

```sh
sudo gitstack-backup.timer — every day at 02:00
```

To change the hour, edit the timer unit and reload systemd.

To disable the timer:

```sh
sudo systemctl disable --now gitstack-backup.timer
```

### Copying backups off the machine

Backups are regular files in the configured destination directory. You can
copy them with `scp`, `rsync`, or any other tool:

```sh
# From a remote machine:
scp gitstack@homehub:/var/backups/gitstack/gitstack-backup-*.tar.gz ./local-backups/

# With encryption key (if the backup was encrypted):
scp gitstack@homehub:/var/backups/gitstack/chiaave.txt ./
```

If you use a separate backup disk, mount it and copy archives to an external
medium or a remote storage service.

---

## Restore

`gitstack restore` restores an archive on a clean installation of the
**same server version**. The database, repositories, attachments, secrets, and
configuration files are restored to a coherent state.

### Prerequisites

1. GitStack must be installed on the target machine (run `deploy/install.sh`).
2. The installed version must match the version in the backup archive
   (`image_tag` in `config.yaml` must equal the archive's `version`).
3. If the archive was encrypted, you need the key file.

### Restoring a backup

```sh
sudo gitstack restore /var/backups/gitstack/gitstack-backup-20261006T143000Z-sha-f4f3a2b.tar.gz
```

**With an encrypted archive:**

```sh
sudo gitstack restore --key-file chiave.txt /var/backups/gitstack/gitstack-backup-20261006T143000Z-sha-f4f3a2b.tar.gz
```

**Override the working directory (where the stage is created):**

```sh
sudo gitstack restore --dest /mnt/backup /path/to/backup.tar.gz
```

### What happens during restore

1. The archive is opened and its manifest is read and verified.
2. The archive is extracted to a temporary stage directory in the destination
   directory (mode `0700`).
3. Each file is verified against the manifest (size and SHA-256).
4. All services that use the database or volumes (`gateway`, `git`, `core`,
   `identity`) are scaled to zero — they stay stopped until the restore is
   complete.
5. The database is restored: the `identity` and `core` schemas are dropped and
   repopulated from `database.sql`.
6. The Git repositories are restored from `git-data.tar` (the `git-data` volume
   is emptied then repopulated).
7. Attachments are restored from `attachments.tar` (if present).
8. Kubernetes secrets are restored from `secrets.json` (the Postgres secret is
   **not** restored — its password is the one from the new installation).
9. Configuration files under `config/` are restored to `/etc/gitstack/`, except
   the `config.yaml` itself (which remains the one from the new installation).
10. The services are scaled back up and the installer waits for all of them to
    become Ready.

If any step fails, the services **stay stopped** (a half-restored database
must not receive traffic) and the error message explains how to restart them
manually. Restored SSH host keys prevent the "host key changed" warning for
clients; existing sessions and tokens continue to work because identity finds
its service secret and OIDC key again.

### Known limitations

- External Postgres (`postgres.enabled: false` in the chart values) is not
  supported for restore (the dump comes from inside the Postgres pod).
- Only `local-path` volumes (on-host folders) are supported.

### Monthly restore drill

We recommend running a restore drill at least once a month:

1. Install GitStack version X, create test users, organizations, repositories
   (with pushes via SSH), issues with attachments, and note the repository
   SHAs (`git ls-remote`). Keep a reference clone.
2. Run a backup: `sudo gitstack backup --key-file k`. Verify the archive
   (`ls -l` — archive is `0600`, directory is `0700`, `.sha256` and
   `manifest.json` exist; for an unencrypted archive: `tar -xzOf archive.tar.gz manifest.json`).
3. Copy the archive off the machine.
4. Clean the machine (uninstall k3s, remove `/var/lib/rancher`) and reinstall
   the same version X.
5. Restore: `sudo gitstack restore --key-file k archive.tar.gz`. After login,
   verify that organizations, repositories, issues, attachments, and `git
   clone` (HTTP and SSH, without a new host-key warning) match the references
   from step 1.

---

## Upgrade

`gitstack upgrade` updates a GitStack installation to a newer version with
automatic pre-backup, health check, and rollback on failure.

### How it works

The upgrade follows these steps:

1. **Checks** (nothing changes if any check fails, exit code 6):
   - The target version exists (GitHub API); it is not a downgrade (the
     installed version is compared against the target: "behind" or diverged
     histories are rejected — downgrade is not supported because database
     migrations cannot be undone).
   - GitStack is healthy right now.
   - The `gitstack-linux-amd64` binary from the release `sha-<commit>` is
     downloaded and its SHA-256 matches (V5).
   - The `deploy/gitstack` chart from the target commit is downloaded.
   - Every image the chart would use exists in its registry (verified via
     `helm template` with current values).
   - `--dry-run` stops here.

2. **Pre-backup** using the same logic as `gitstack backup`
   (`--dest`, `--key-file`).

3. **`helm upgrade --wait`** of the downloaded chart, with the values chosen
   at installation (`helm get values` re-applied to the new chart defaults
   using `--reuse-values` would lose new keys). Migrations run on service
   startup. Then waits for all services and the API to become healthy,
   up to `--timeout` (default 10 minutes).

4. If everything is healthy: atomic replacement of the binary
   (`/usr/local/bin/gitstack`), the local chart copy (`chart_dir`), and
   `image_tag` in the config.

### Rollback

If any step after the backup fails (including Ctrl-C or a mid-way error):

1. The binary and the local chart copy are restored.
2. `helm rollback` to the previous revision.
3. **If migrations touched the database** (schema versions in
   `identity.schema_migrations` and `core.schema_migrations` have changed
   or are dirty, or unreadable), `gitstack restore` of the pre-backup is
   performed. If the database was not touched, nothing is restored: data
   written in the meantime is kept. With the restore, data written after
   the backup during the upgrade is lost — the final message says so.

The exit codes are:

| Code | Meaning |
|---|---|
| 0 | Upgraded successfully, all services healthy |
| 6 | Rejected by checks (nothing was modified) |
| 7 | Upgrade failed, rollback succeeded |
| 8 | Upgrade failed and rollback did not succeed: manual intervention needed |

### Usage

```sh
# Upgrade to the latest commit on main:
sudo gitstack upgrade

# Upgrade to a specific version or commit SHA:
sudo gitstack upgrade --to sha-abc123def

# Dry run (checks only, no changes):
sudo gitstack upgrade --dry-run

# With custom timeout, backup destination, and extra Helm values:
sudo gitstack upgrade --to sha-abc123def --dest /mnt/backup/gitstack \
  --timeout 20m --key-file chiave.txt \
  --set gateway.replicaCount=2 --values extra-values.yaml
```

### What happens if the upgrade fails

- **Before the backup**: nothing is modified. Fix the cause and retry.
- **After the backup but before `helm upgrade`**: the backup is preserved and
  the binary is not changed.
- **During or after `helm upgrade`** (the most likely failure point):
  automatic rollback is attempted. If the rollback succeeds (exit 7) the
  system is back to a healthy state with the old version. If the rollback
  fails (exit 8), the message tells you the commands to finish manually.

---

## Diagnostics

### `gitstack status`

```sh
gitstack status [--json] [--config PATH]
```

Displays the version, host, service health, and last backup. Without
`--json` it prints a human-readable report:

```
GitStack
  Versione server: sha-f4f3a2b
  Versione gitstack: sha-f4f3a2b
  Host: homehub.local (SSH git: porta 2222)
  Release: gitstack (namespace default)

Servizi
  core                         1/1 pronti  OK
  gateway                      1/1 pronti  OK
  git                          1/1 pronti  OK
  identity                     1/1 pronti  OK
  api (gateway /healthz)        OK (https://homehub.local/api/healthz: HTTP 200)

Ultimo backup: ultimo successo: 2026-10-06 02:00:15 (/var/backups/gitstack/gitstack-backup-20261006T020015Z-sha-f4f3a2b.tar.gz)

Stato: sano
```

With `--json` it outputs a JSON object with `binary_version` and `report`
fields.

Exit codes:

| Code | Meaning |
|---|---|
| 0 | All services and the API are healthy |
| 1 | At least one service or the API is not healthy |
| 2 | Usage error |
| 3 | Configuration file missing or invalid |
| 4 | The cluster cannot be queried |
| 5 | Root required (only for commands that change state) |

### Reading service status manually

```sh
# Kubernetes pods:
KUBECONFIG=/etc/rancher/k3s/k3s.yaml k3s kubectl get pods -n default

# Helm release:
KUBECONFIG=/etc/rancher/k3s/k3s.yaml k3s kubectl -n default get secret gitstack-identity-admin -o jsonpath='{.data.password}' | base64 -d; echo

# API health from a client:
curl -i https://homehub.local/api/healthz

# Helm status:
KUBECONFIG=/etc/rancher/k3s/k3s.yaml helm status gitstack -n default
```

### Reading the admin password

```sh
KUBECONFIG=/etc/rancher/k3s/k3s.yaml k3s kubectl -n default get secret gitstack-identity-admin -o jsonpath='{.data.password}' | base64 -d; echo
```

### Checking k3s logs

```sh
journalctl -u k3s -f
```

### Checking the last backup state

The last backup result is stored in
`/etc/gitstack/backup-state.json`:

```json
{
  "success": true,
  "path": "/var/backups/gitstack/gitstack-backup-20261006T020015Z-sha-f4f3a2b.tar.gz",
  "at": "2026-10-06T02:00:15Z"
}
```

On failure, the `error` field contains the error message.

### Configuration file

The installation configuration is at `/etc/gitstack/config.yaml` (mode
`0600`):

```yaml
version: 1
host: homehub.local
ssh_port: 2222
tls: internal
ca_cert: /etc/gitstack/tls/ca.crt
chart_dir: /usr/local/share/gitstack/chart
release: gitstack
namespace: default
image_tag: sha-f4f3a2b
kubeconfig: /etc/rancher/k3s/k3s.yaml
backup:
  destination: /var/backups/gitstack
  retention: 7
```

### Changing the hostname

If the hostname changes (for example after a network reconfiguration):

```sh
sudo gitstack config set host newname.local
```

This regenerates the certificate (internal CA mode), re-deploys the services
with the new public URL, and updates the configuration file.

### Version

```sh
gitstack version
```

Prints the binary version (normally the same as `image_tag` in the config,
which is the commit tag of the deployed images).

---

## Quick reference: most used commands

| Command | Description |
|---|---|
| `sudo ./deploy/install.sh` | Install or reinstall GitStack |
| `sudo gitstack status` | Check the health of the installation |
| `sudo gitstack version` | Show the binary version |
| `sudo gitstack backup` | Create a backup in the configured destination |
| `sudo gitstack backup --key-file k` | Create an encrypted backup |
| `sudo gitstack restore archive.tar.gz` | Restore an archive on the same version |
| `sudo gitstack restore --key-file k archive.tar.gz` | Restore an encrypted archive |
| `sudo gitstack upgrade` | Upgrade to the latest commit on main |
| `sudo gitstack upgrade --dry-run` | Check if an upgrade would succeed |
| `sudo gitstack config set host NAME` | Change the hostname (re-generates the certificate) |
| `journalctl -u k3s -f` | Stream k3s logs |

---

*This guide is T-09 of milestone M-08 [c_8458909a21d9035f]. Commands and
options are taken from the code on main (install.sh, admin/, config).*
