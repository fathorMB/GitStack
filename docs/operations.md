# Operations Guide — GitStack

> **Scope:** This guide covers installation, backup, restore, upgrade, push
> mirrors, and diagnostics of a GitStack instance deployed on a single Linux host with k3s.
> It is written for operators who manage the machine directly (for example the
> `homehub` appliance described in M-08 [c_8458909a21d9035f]).
>
> **Supported systems:** Ubuntu Server 22.04 and 24.04 LTS, Pop!_OS 22.04 and 24.04 (all amd64). Other systems require `--force`.

## Installation

### Prerequisites

GitStack runs on a clean machine with one of the following supported systems (all amd64):

- Ubuntu Server 22.04 LTS
- Ubuntu Server 24.04 LTS
- Pop!_OS 22.04
- Pop!_OS 24.04

Other distributions and WSL2 are not yet supported.

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
2. Installs k3s (pinned in `deploy/k3s-version`, today `v1.36.4+k3s1`) and Helm (`v3.16.3`, pinned).
3. Generates an internal CA and a server certificate (see [HTTPS](#https-modes)).
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
| `--version vX.Y.Z` | `GITSTACK_VERSION` | (none) | Install a released version (also `vX.Y.Z-rc.N`): image tag, chart and `gitstack` binary come from the release of that name, signatures verified (V5). Cannot be combined with a different `--image-tag`. See "Rilascio con versione". |
| `--image-registry HOST` | `GITSTACK_IMAGE_REGISTRY` | (from chart) | Container registry for GitStack images. Useful for a mirror. |
| `--admin-binary PATH\|URL` | `GITSTACK_ADMIN_BINARY` | (download from release) | Path or URL of the `gitstack` admin binary; its SHA-256 is verified. |
| `--admin-sha256 HEX` | `GITSTACK_ADMIN_SHA256` | (from `<binary>.sha256`) | Expected SHA-256 of the admin binary. |
| `--host NAME\|IP` | — | (auto-detected) | Hostname or IP that clients use to reach GitStack. Repeatable — all values become SANs in the certificate. The first is the public URL. The installer resolves the name and verifies it points to a local address. |
| `--tls internal` | `GITSTACK_TLS_MODE=internal` | `internal` | Internal CA (default). The installer generates a CA whose key stays on the host only. |
| `--tls letsencrypt` | `GITSTACK_TLS_MODE=letsencrypt` | — | Public certificate from Let's Encrypt (HTTP-01 challenge). Requires `--host` pointing to a DNS name reachable on the internet on port 80. |
| `--tls-cert FILE --tls-key FILE` | `GITSTACK_TLS_CERT`, `GITSTACK_TLS_KEY` | — | Client-supplied PEM certificate and key. |
| `--tls-email EMAIL` | `GITSTACK_TLS_EMAIL` | — | Email for Let's Encrypt (recommended). |
| `--insecure-http` | `GITSTACK_TLS_MODE=insecure` | — | HTTP only, no TLS. **Only for local tests**; the web UI shows a warning. |
| `--postgres-url URL` | `GITSTACK_POSTGRES_URL` | — | Use an external PostgreSQL 15–17 instead of the bundled one (see [External PostgreSQL](#external-postgresql)). |
| `--postgres-password-file FILE` | `GITSTACK_POSTGRES_PASSWORD_FILE`, or `GITSTACK_POSTGRES_PASSWORD` | — | Password of the database user. Keep it out of the command line. |
| `--postgres-ca-file FILE` | `GITSTACK_POSTGRES_CA_FILE` | — | PEM CA that signed the database server certificate (private CA with `verify-ca`/`verify-full`). |
| `--values FILE` | — | — | Additional Helm values file (`-f`). Repeatable. |
| `--set KEY=VALUE` | — | — | Additional Helm value (`--set`). Repeatable. |
| `--http-proxy URL` | `GITSTACK_HTTP_PROXY` | — | Proxy for everything the server downloads and for the GitStack pods. See [Corporate network](#corporate-network-proxy-mirrors-and-ca). |
| `--https-proxy URL` | `GITSTACK_HTTPS_PROXY` | (the HTTP proxy) | Proxy for `https://` destinations. |
| `--no-proxy LIST` | `GITSTACK_NO_PROXY` | — | Comma-separated destinations that bypass the proxy (added to the cluster networks and suffixes, which never use it). |
| `--registry-mirror URL` | `GITSTACK_REGISTRY_MIRROR` | — | Mirror for **all** images, k3s system images included (`registries.yaml`). |
| `--registry-username U`, `--registry-password-file FILE` | `GITSTACK_REGISTRY_USERNAME`, `GITSTACK_REGISTRY_PASSWORD` | — | Optional mirror credentials. The password is never a command-line argument. |
| `--download-mirror URL` | `GITSTACK_DOWNLOAD_MIRROR` | — | Mirror for the k3s, Helm and `gitstack` downloads. |
| `--ca-bundle FILE` | `GITSTACK_CA_BUNDLE` | — | Corporate CA (PEM): trusted by the system, by containerd and by the pods. |

Other environment variables: `INSTALL_K3S_VERSION`,
`GITSTACK_BACKUP_DIR`, `GITSTACK_BACKUP_RETENTION`,
`GITSTACK_BACKUP_TIMER_HOUR` (0–23, default `02`).

### External PostgreSQL

By default GitStack runs its own PostgreSQL inside the cluster. To use the
customer's server instead (N8), PostgreSQL **15, 16 or 17** is supported.

**1. The DBA prepares the database once.** The installer needs a dedicated
database and a user that can create schemas and roles (not a superuser).
`deploy/postgres-prepare.sql` does it, idempotently:

```sh
export GITSTACK_DB_PASSWORD='<password for the GitStack user>'   # letters, digits and - . _ ~
psql "postgres://postgres@db.example.com:5432/postgres?sslmode=require" \
     -v ON_ERROR_STOP=1 -f deploy/postgres-prepare.sql
```

It creates the `gitstack` user (`LOGIN CREATEROLE`) and the `gitstack`
database it owns (`GITSTACK_DB_NAME` and `GITSTACK_DB_USER` change the
names). It never grants superuser. Do not revoke `CONNECT` from `PUBLIC`:
the application role that GitStack creates for identity must connect.

**2. Install.** The password goes in a file (or in `GITSTACK_POSTGRES_PASSWORD`),
not on the command line:

```sh
sudo ./deploy/install.sh \
  --postgres-url 'postgres://gitstack@db.example.com:5432/gitstack?sslmode=verify-full' \
  --postgres-password-file /root/gitstack-db.pw \
  --postgres-ca-file /root/db-ca.crt
```

`sslmode` defaults to `require`; the CA is optional (needed for
`verify-ca`/`verify-full` with a private CA); `sslmode=disable` only on a
trusted network. Before touching the machine the installer runs the
**preflight** (`gitstack postgres check`): connection, supported version
(15–17), and the permission to create schemas and roles. Each failure says
what to do, e.g. `PostgreSQL 14.12 non supportato` or `l'utente "x" non può
creare ruoli (manca CREATEROLE)`. The same check can be run any time:

```sh
gitstack postgres check --url 'postgres://gitstack@db.example.com:5432/gitstack?sslmode=require' \
  --password-file /root/gitstack-db.pw [--ca-file /root/db-ca.crt] [--json]
```

(exit code `11` when the database is not suitable.) The password and CA are
stored as Kubernetes Secrets (`<release>-postgres-external`,
`<release>-postgres-ca`) managed outside Helm; `config.yaml` records host,
database, user, `sslmode` and the detected version, never the password.

What GitStack creates in the database, as with the bundled PostgreSQL: the
`core` schema (owned by the configured user), the `identity_app` role and
the `identity` schema it owns. Nothing outside these schemas is touched.

**Operations work the same.** `gitstack backup` dumps the `identity` and
`core` schemas from the external database (through the `<release>-pgclient`
pod, a small `psql`/`pg_dump` pod the chart adds), `gitstack restore`
recreates them in a single transaction, `gitstack upgrade` takes the same
pre-upgrade backup and, on failure, restores the schemas. Backups are copied
off the machine as usual
([Copying backups off the machine](#copying-backups-off-the-machine)).
Notes:

- The pod client has the **same major version as the server**
  (`postgres.external.client.image.tag`, set by the installer). If you upgrade
  PostgreSQL's major version, change that tag too: `gitstack status` warns when
  they differ.
- A restore needs the same database user that owned the objects at backup time.
- The database password must be URL-safe (letters, digits, `- . _ ~`) because
  services receive it inside a connection URL.
- Re-running `install.sh` on an external-database installation requires the
  same `--postgres-*` options; without them it stops instead of switching to
  the bundled PostgreSQL.
- An installation made earlier with `--set postgres.enabled=false` is adopted
  by re-running the installer with the `--postgres-*` options.

`gitstack status` shows the database (see [`gitstack status`](#gitstack-status)).

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
sudo KUBECONFIG=/etc/rancher/k3s/k3s.yaml k3s kubectl -n default get secret gitstack-identity-admin -o jsonpath='{.data.password}' | base64 -d; echo
```

On first login the admin must change this password.

### Corporate network: proxy, mirrors and CA

For a server that has no direct way out (rules N4 and N9). Every option is
optional and they combine; the installer remembers them for the next run (the
`network:` section of `/etc/gitstack/config.yaml`).

```sh
sudo ./deploy/install.sh \
  --http-proxy http://proxy.corp:3128 --no-proxy registry.corp,git.corp \
  --registry-mirror https://registry.corp:5000 \
  --registry-username puller --registry-password-file /root/registry.pw \
  --download-mirror https://files.corp/gitstack \
  --ca-bundle /root/corp-ca.pem
```

| What | Where it applies |
|---|---|
| `--http-proxy`, `--https-proxy`, `--no-proxy` | The installer's own downloads (curl, the k3s script, Helm); **k3s and containerd** (systemd drop-in `/etc/systemd/system/k3s.service.d/10-gitstack-network.conf`); the **GitStack pods** (chart values `proxy.http`, `proxy.https`, `proxy.noProxy`: webhooks, mirrors, OIDC, SMTP). `NO_PROXY` for k3s always includes `localhost`, `10.42.0.0/16`, `10.43.0.0/16`, `.svc`, `.cluster.local` and the machine's own IPv4 addresses (the API server reaches the kubelet on `<node IP>:10250`; if the address changes, run `gitstack config set network --no-proxy ...` again to refresh it). Without `--https-proxy` the HTTP proxy is used for `https://` too. |
| `--registry-mirror` | `/etc/rancher/k3s/registries.yaml` (mode `0600`) with a `"*"` mirror: every image is requested from the mirror first, including k3s system images (pause, coredns, traefik...). The mirror must serve images under their original name without the registry host (`ghcr.io/fathormb/gitstack-core` is `fathormb/gitstack-core`), as a pull-through cache or a registry you filled yourself. With `--ca-bundle` and an `https://` mirror, `registries.yaml` also trusts the CA. |
| `--download-mirror` | See the layout below. |
| `--ca-bundle` | Copied to `/etc/gitstack/network/ca.crt`; added to the system trust store (`update-ca-certificates`); referenced by `registries.yaml`; ConfigMap `<release>-corporate-ca` (key `ca.crt`) mounted in core, git and identity (chart value `corporateCA.configMap`). The ConfigMap holds the system CAs **plus** the corporate CA: for git mirrors the chart *replaces* the system CAs with this file, so a mirror to a public host reached directly (in `--no-proxy`) keeps trusting the public CAs. The Go services trust it in addition to their own CAs. After a system CA update, re-run `install.sh` or `gitstack config set network --ca-bundle FILE` to refresh the ConfigMap. |

**Download mirror layout** (paths under the URL you give):

| Path | Content |
|---|---|
| `k3s/install.sh` | The k3s install script (from `https://get.k3s.io`) |
| `k3s/releases/download/<version>/k3s` and `sha256sum-amd64.txt` | The k3s binary and its checksums (what the script downloads from GitHub releases) |
| `helm/helm-<version>-linux-amd64.tar.gz` and `.sha256sum` | The Helm tarball |
| `gitstack/<image-tag>/gitstack-linux-amd64`, `.sha256` and `.sigstore.json` | The `gitstack` binary, its checksum and its cosign bundle (V5) |
| `gitstack/<image-tag>/gitstack-<chart-version>.tgz` and `.sigstore.json` | The signed Helm chart package and its cosign bundle (V5) |
| `cosign/<version>/cosign-linux-amd64` | The pinned cosign (`v2.4.0`; its SHA-256 is still checked) |
| `gitstack/source/<sha>.tar.gz`, `gitstack/source/<ref>.sha` | Repository source and the commit of a ref (only when running the script without a checkout) |

The signature rules do not change (V5): k3s and Helm are verified against their
SHA-256 checksums fetched from the same mirror, and the `gitstack` binary, the
chart and the `gitstack-*` images are verified with cosign exactly as when they
come from GitHub (same workflow identity and OIDC issuer, bundle required): a
mirror must therefore carry the `.sigstore.json` bundles above and, for the
images, the signatures (a pull-through cache of `ghcr.io` has them; a registry
you filled yourself needs them copied with the images, for example with
`cosign copy`). The images are verified on the `--registry-mirror` under their
original name without the host, with the mirror credentials, and
`--allow-insecure-registry` for an `http://` mirror. The proxy and the
corporate CA apply to cosign as to the other downloads. A mirror without
signatures is refused unless you state the exception explicitly with
`--allow-unsigned-mirrors` (see V5 below); for a stronger guarantee on the
binary also pass `--admin-sha256` from a trusted channel.
Outbound rules (C8) do not change either: the GitStack services apply
`egress.*` to the real destination before talking to the proxy.

**Preflight.** Before touching the system, the installer tries to reach the
proxy, the mirrors and (without a mirror) the usual sources (`get.k3s.io`, GitHub
releases, `get.helm.sh`, `ghcr.io`, Docker Hub, `registry.k8s.io`), through the
proxy, and lists what does not answer:

```
ERRORE: preflight di rete: non rispondono:
  - mirror delle immagini: https://registry.corp:5000/v2/ non risponde (via proxy http://proxy.corp:3128)
  - proxy: http://proxy.corp:3128 non raggiungibile (proxy.corp:3128)
```

With any network option a failure stops the installation; without options it is
only a warning. A mirror that answers 401 to the supplied credentials is also
reported. Internal hosts (the mirror itself, usually) belong in `--no-proxy`
unless the proxy can reach them.

**Changing it later.** `gitstack config set network` (root) changes the same
settings. It checks reachability first and changes nothing if something does
not answer (`--force` applies anyway), regenerates `registries.yaml`, the k3s
drop-in and the CA copies, restarts k3s only if those files changed (and waits
for the node to be `Ready`), then updates the chart (`helm upgrade
--reuse-values`) and finally `config.yaml`.

```sh
sudo gitstack config set network --no-proxy registry.corp,git.corp,10.1.0.0/16
sudo gitstack config set network --registry-mirror https://registry2.corp:5000 \
  --registry-username puller --registry-password-file /root/registry.pw
sudo gitstack config set network --http-proxy ""      # remove the proxy
sudo gitstack config set network --clear              # remove everything
```

An option given with an empty value removes that setting. `gitstack upgrade`
keeps the network values (they are Helm values) and uses the configured proxy
for its own downloads. This includes the k3s update step (N10): with a
`--download-mirror` configured, `gitstack upgrade` fetches the k3s install script
and binary from `<mirror>/k3s/install.sh` and `<mirror>/k3s/releases` (the same
layout as the installer), not from `get.k3s.io`; afterwards it removes the
`*_PROXY` lines that the k3s script copies into `k3s.service.env`, so the systemd
drop-in stays the only source. The mirror must therefore also hold the k3s
version declared in `deploy/k3s-version` of the version you upgrade to.

**External PostgreSQL (N8) on a corporate network.** The database speaks the
PostgreSQL protocol over plain TCP, not HTTP: the proxy settings do not apply to
it. The installer's Postgres preflight and the services connect to the database
directly, and the network preflight does not probe it, so the database host
needs no `--no-proxy` entry. Its CA (`--postgres-ca-file`, Secret `caSecret`,
mounted at `/etc/gitstack/postgres-ca` and passed as `sslrootcert`) and the
corporate CA (`corporateCA.configMap`, mounted at `/etc/gitstack/corporate-ca`,
`SSL_CERT_DIR`) are separate volumes and do not override each other.

**Test.** `deploy/test-network/run.sh all` (`seed`, then `verify`) installs
GitStack on the Hyper-V test VM of `deploy/test-vm` (Ubuntu Server 24.04),
restored to its `clean` checkpoint before every run. The VM has no direct way
out: iptables rules in `OUTPUT` let through only the proxy and the mirror on the
host. On the host, Docker runs a Squid proxy (which in `verify` denies the image
registries, so images can only come from the mirror) and a `registry:2` mirror;
`seed` fills the mirror with the images k3s really pulled. `verify` checks the
closed network, the preflight failure, the installation through proxy and mirror,
what the proxy and the registry logged, and `gitstack config set network`.

Requirements: Hyper-V with the test VM created by `deploy/test-vm/new-vm.ps1`,
PowerShell as administrator (the script restores the checkpoint with
`reset-vm.ps1`), Docker (Linux engine), `ssh`, `curl` and `go`. Variables:
`GITSTACK_TEST_VM_NAME` (default `gitstack-test-vm`), `GITSTACK_TEST_VM_KEY`
(SSH private key, default `~/.ssh/gitstack_vm`), `GITSTACK_TEST_HOST_IP` (the
host address as seen by the VM; default: the host address on the Default
Switch). A container as the node was discarded: on Docker Desktop/WSL2 the
kernel uses cgroup v1 and the k3s kubelet does not start there. The test does
not run in CI. Unit tests: `deploy/tests/network_test.sh` (installer) and
`admin/internal/cli/confignetwork_test.go` (`config set network`).

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

### Backup to S3 (N7)

In addition to the local copy, every backup can be uploaded to any
S3-compatible storage (AWS S3, MinIO, Backblaze B2, Wasabi, a NAS with an S3
API). The daily backup first creates the archive locally and then uploads it.

**Archives uploaded to S3 are always encrypted** with the same key as
`--key-file`: a configuration without a key file is refused (by
`gitstack config set backup.s3` and by `install.sh`), and
`gitstack backup` never uploads an unencrypted archive. With S3
configured, the local archive is encrypted too, with the key from
`backup.s3.key_file`, so the timer needs no arguments.

**Configure it after installation:**

```sh
export GITSTACK_S3_ACCESS_KEY=... GITSTACK_S3_SECRET_KEY=...
openssl rand -base64 32 | sudo tee /etc/gitstack/backup.key >/dev/null
sudo chmod 0600 /etc/gitstack/backup.key
sudo -E gitstack config set backup.s3 \
  --endpoint s3.eu-central-003.backblazeb2.com --bucket gitstack-backups \
  --prefix prod --region eu-central-003 --key-file /etc/gitstack/backup.key
```

**Or at install time** (the access keys only from the environment, never as
arguments, so they do not show up in `ps`):

```sh
sudo GITSTACK_S3_ACCESS_KEY=... GITSTACK_S3_SECRET_KEY=... ./deploy/install.sh \
  --s3-endpoint s3.example.com --s3-bucket gitstack-backups \
  --s3-key-file /etc/gitstack/backup.key
```

The settings live in the `backup.s3` section of
`/etc/gitstack/config.yaml` (owner root, mode `0600`, the file is
refused otherwise): `endpoint` (host[:port], or a URL with `http://` for a
MinIO without TLS), `bucket`, `prefix`, `region` (default `us-east-1`),
`access_key`, `secret_key`, `key_file`, `retention` and `notify_command`.
`gitstack config set backup.s3` checks the bucket before writing
(`--no-check` skips it) and `--remove` drops the section. The access keys
are never printed or logged: errors from the S3 client are scrubbed before they
reach the terminal, `gitstack status`, the state file or a notification.
The archive contains a copy of `/etc/gitstack/`, which holds the keys:
that is one more reason the archive is always encrypted.

**Retention on S3** is separate from the local one:

| `backup.s3.retention` | Effect |
|---|---|
| (not set) | keep the newest **30** archives on S3 |
| a number `N` | keep the newest N archives (and remove the older ones with their `.sha256`) |
| `bucket` | never delete anything: leave it to the bucket's lifecycle rules |

Only objects named `gitstack-backup-*.tar.gz[.enc]` under the prefix are
considered. The local retention (`backup.retention`, default 7) is unchanged.

**If the upload fails** the local backup is still valid and kept. The command
exits with code `10` (so `systemctl status gitstack-backup.service` shows the
failure), `gitstack status` prints `Backup su S3: ULTIMO CARICAMENTO FALLITO`
with the cause (JSON: `backup_upload_failed`), and, if set,
`backup.s3.notify_command` is run with `sh -c` and these variables:
`GITSTACK_BACKUP_EVENT=s3-upload-failed`, `GITSTACK_BACKUP_ARCHIVE`,
`GITSTACK_BACKUP_ERROR`, `GITSTACK_HOST`. Example, with mail:

```yaml
backup:
  s3:
    notify_command: 'echo "$GITSTACK_BACKUP_ERROR" | mail -s "GitStack: backup S3 fallito" admin@example.com'
```

(The administrators' notification does not go through the core internal API:
the admin tool runs on the host, outside the cluster, and has no credential
for it; `notify_command` reaches whatever channel the administrators already
use.)

**Restore on a new machine (N3):** install the same GitStack version with
`install.sh`, giving the same `--s3-*` options and the key file, then:

```sh
sudo gitstack restore --from s3            # the newest archive on S3
sudo gitstack restore --from s3 gitstack-backup-20261006T143000Z-sha-f4f3a2b.tar.gz.enc
```

If you ran `gitstack backup --key-file X` with a key different from
`backup.s3.key_file`, the uploaded archive is encrypted with X (a warning is
printed at upload time): `restore --from s3` then needs `--key-file X`, because
without it the key from `backup.s3.key_file` is used and fails.

The archive is downloaded into a temporary folder inside the backup
destination (mode `0700`), verified against its `.sha256`, restored and
removed.

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
| `6` | Restore refused — version mismatch, corrupted archive, wrong checksum, or encrypted archive without `--key-file` |
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
   health, binary with checksum, chart and images exist, and the k3s
   version the target declares (see *Upgrading k3s*). If any check
   fails, exit `6` (`ExitRefused`), nothing changed.
2. **Preventive backup** of the current state (same options as
   `gitstack backup`).
2b. **k3s**, only if the target declares another version: patch in
   automatic, minor with `--k3s`; short outage announced.
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
| `--version vX.Y.Z` | Target released version (`vX.Y.Z` or `vX.Y.Z-rc.N` only): same as `--to`, but strict SemVer; excludes `--to` | — |
| `--dest DIR` | Override backup destination directory | `backup.destination` from config |
| `--key-file FILE` | Path to AES-256-GCM encryption key | none (unencrypted backup) |
| `--timeout D` | Max time for service health checks | `10m` |
| `--k3s` | Allow updating k3s to a new minor (one at a time); patches need no flag | off |
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

### Upgrading k3s (rule N10)

Each GitStack version declares the k3s version it was tested with in
`deploy/k3s-version` (one line, e.g. `v1.36.4+k3s1`). `install.sh` installs
that version and `gitstack upgrade` compares it with the one running
(`k3s --version`; `gitstack status` shows both):

| Change | What `gitstack upgrade` does |
|---|---|
| Same version | nothing to do for k3s |
| **Patch** (`v1.36.4` → `v1.36.7`, or a new `+k3sN` revision) | updates k3s **automatically**, after the preventive backup, announcing a short outage |
| **Minor** (`v1.36` → `v1.37`) | refused without the flag, with the explanation; with `--k3s` it proceeds, one minor at a time |
| More than one minor ahead | refused even with `--k3s`: upgrade to a GitStack version tested with the intermediate minor first |
| k3s newer than the declared one | refused: k3s is never downgraded automatically (see below) |

Order of the steps (always): **preventive backup → k3s → GitStack**. The checks
come first and change nothing, so a refusal leaves the system untouched.
While k3s restarts, all services stop for a few minutes: schedule the upgrade
accordingly. `--dry-run` (add `--k3s` to see a minor) prints the plan.

```sh
sudo gitstack upgrade --dry-run --k3s   # what would change
sudo gitstack upgrade --k3s             # k3s minor + GitStack
```

If the k3s step fails, GitStack is **not** upgraded (the chart stays as it
was), the command exits `70` and prints the path of the preventive backup.
There is no automatic rollback of k3s: follow the procedure below.

### Hardening k3s: private kubeconfig and encrypted Secrets (F2, GIT-228)

`/etc/rancher/k3s/k3s.yaml` is cluster-admin: whoever reads it reads every
Secret (database password, service secret, webhook and OIDC keys, admin
password). Since GIT-228 the installer starts k3s with

```
server --write-kubeconfig-mode 600 --secrets-encryption
```

- the kubeconfig is `root:root` mode `600`: `kubectl`, `k3s kubectl` and
  `helm` need `sudo` (or `sudo -i`); `gitstack` already runs as root;
- the Secrets are encrypted at rest in the k3s datastore (AES-CBC, key in
  `/var/lib/rancher/k3s/server/cred/encryption-config.json`, root only). The key
  lives on the same disk: it protects copies of the datastore and backups of
  the disk, not an attacker who is already root;
- after an install check with `sudo k3s secrets-encrypt status`
  (`Encryption Status: Enabled`) and `stat -c %a /etc/rancher/k3s/k3s.yaml` (`600`).
  `install.sh` makes both checks and stops if they fail.

**Existing installations.** An installation made before GIT-228 has mode 644 and
plain Secrets. `sudo gitstack upgrade` fixes it, also when GitStack is already at
the requested version (`--dry-run` shows the plan: "Hardening di k3s"):

1. health check and preventive backup, as in every upgrade;
2. a copy of the unit is kept in `/etc/systemd/system/k3s.service.pre-hardening`,
   then `k3s.service` is rewritten (mode `600`, `--secrets-encryption`);
3. `systemctl daemon-reload`, `systemctl restart k3s` and wait until all
   services are healthy (a short interruption, like a k3s patch update);
4. `k3s secrets-encrypt status` must say `Enabled`; then
   `k3s secrets-encrypt rotate-keys` re-encrypts the Secrets that were in plain text;
5. if step 3 or the check in step 4 fails, the unit is put back as it was, k3s is
   restarted and the upgrade stops with exit code `70` before touching GitStack.
   Once the encryption is active the flag is **not** removed automatically:
   Secrets written after that would become unreadable.

**When the same upgrade also updates k3s** (the usual case): the k3s installer
script rewrites `k3s.service` itself with the new arguments (mode `600`,
`--secrets-encryption`) and restarts k3s. `gitstack upgrade` then does only the
encryption phase: `k3s secrets-encrypt status` must say `Enabled`, then
`rotate-keys` re-encrypts the Secrets that were in plain text, before GitStack
itself is updated. If `status` does not say `Enabled`, the upgrade stops before
the chart (exit code `70`, the preventive backup is printed). There is **no
automatic rollback of the unit** in this case, because k3s is already at the new
version (see "Rolling back k3s"): fix the cause with `journalctl -u k3s`, check
`sudo k3s secrets-encrypt status`, then run `sudo gitstack upgrade` again.

If `rotate-keys` fails, encryption is active for new writes but the old Secrets are
still plain: run `sudo k3s secrets-encrypt rotate-keys` again and check
`k3s secrets-encrypt status` (`Current Rotation Stage: reencrypt_finished`).

**Manual procedure** (unit in a format `gitstack upgrade` does not recognise, or a
k3s not managed by `gitstack`). Take a backup first (`sudo gitstack backup`), then:

```sh
sudo cp /etc/systemd/system/k3s.service /root/k3s.service.pre-hardening
# edit ExecStart: '--write-kubeconfig-mode' '600' and '--secrets-encryption'
sudo systemctl daemon-reload && sudo systemctl restart k3s
sudo k3s secrets-encrypt status          # Encryption Status: Enabled
sudo k3s secrets-encrypt rotate-keys     # re-encrypts the existing Secrets
sudo chmod 600 /etc/rancher/k3s/k3s.yaml # if k3s has not rewritten it yet
sudo gitstack status
```

To undo **before** any Secret is written encrypted: restore the saved unit,
`daemon-reload`, `restart k3s`. **After** the encryption is active, first run
`sudo k3s secrets-encrypt disable` and `sudo k3s secrets-encrypt reencrypt`, then
remove the flag from the unit and restart; otherwise restore the backup with
`gitstack restore`.

### Rolling back k3s

Not automatic, on purpose: Kubernetes does not support downgrading the
datastore across minors. Use the preventive backup printed by
`gitstack upgrade` (or the latest in `backup.destination`).

**After a patch update** (same minor): the datastore is compatible, so it is
enough to reinstall the previous version with the same options as the
installer:

```sh
curl -sfL https://get.k3s.io | sudo INSTALL_K3S_VERSION=v1.36.4+k3s1 \
  INSTALL_K3S_EXEC="server --write-kubeconfig-mode 600 --secrets-encryption" sh -
k3s --version            # the version you asked for
sudo gitstack status     # wait until all services are ready
```

**After a minor update**: k3s cannot go back over the existing datastore.
Rebuild the cluster on the old version and restore the backup:

1. Keep the backup archive (and its key, if encrypted) **outside** the cluster
   (`/var/lib/rancher/k3s` is deleted by the next step).
2. `sudo /usr/local/bin/k3s-uninstall.sh` — removes k3s and **all the
   cluster data**, including the volumes of the services.
3. Reinstall GitStack with the *old* GitStack version and its k3s:
   `sudo INSTALL_K3S_VERSION=<old k3s> GITSTACK_IMAGE_TAG=<old tag> ./install.sh`
   from a checkout of that version (the old tag is in the backup file name,
   `sha-...`).
4. `sudo gitstack restore <archive>` (same server version as the archive,
   see *Restore*) and `gitstack status` until the state is `sano`.

Data written after the preventive backup is lost; run `gitstack backup`
first if the instance is still reachable and you want to compare.

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

## Pull mirror

A **pull mirror** is the opposite of the [push mirror](#mirror): GitStack periodically downloads an external Git repository (for example one on GitHub) and keeps a **read-only** copy of it. Uses: a backup copy of a GitHub repository on your own machine, and the first step of moving a team off GitHub.

What it does, and what it deliberately does not do:

- **Prudent copy.** All branches and all tags are copied. GitStack **never deletes and never overwrites** anything. If the origin rewrites a branch (force-push) or a tag, or deletes a ref, the copy on GitStack keeps the previous version, the ref is marked **diverged** and the repository's admins are notified. All the other refs keep updating.
- **Code is read-only, issues are normal.** While the mirror is active every push (HTTPS, SSH, `gs`) is rejected with a message that names the origin (`mirror_read_only`). Only the mirror updates refs. The repository shows a **Mirror** label with the origin and the time of the last update. Issues, labels, milestones and comments work as in any repository.
- **Only Git content.** Issues, pull requests, releases, wikis and labels of the origin are not copied, and neither is Git LFS.
- **Origins.** Only `https://` addresses, without credentials in the address, checked by the same egress protection as webhooks and push mirrors (loopback, cluster and link-local networks are always blocked; see [Destinations on a private network or with a private CA](#destinations-on-a-private-network-or-with-a-private-ca), which applies to pull mirrors too, including `git.mirror.caConfigMap`). SSH and `git://` origins are not supported.
- **Same-address guard.** While a pull mirror is active you cannot configure a push mirror to the same address as its origin.

Creating and managing a pull mirror needs the `admin` role on the repository, or being the installation administrator. The token (optional) is stored encrypted and is never shown again, nor written to logs or to the Git command line. Without the secret encryption key (`GITSTACK_WEBHOOK_SECRET_KEY`) the API answers `503 mirror_secrets_unavailable`.

### Recipe: stage 1, a copy of `github.com/<org>/GitStack` on homehub

Goal: homehub holds an up-to-date, read-only copy of the GitHub repository, as a backup and as the code that dogfooding builds on.

1. **Decide the name.** For example `<org>/gitstack` (the owner can be a user or an organization on homehub); visibility **private** unless you want it public. The name must be free: the import creates the repository.
2. **Credentials.**
   - If `github.com/<org>/GitStack` is **public**, leave username and token empty: the origin is read anonymously.
   - If it is **private**, create a **fine-grained personal access token** on GitHub: *Repository access* → **Only select repositories** → that repository; *Repository permissions* → **Contents: Read-only** (GitHub adds *Metadata: Read-only* by itself). Nothing else, and **no write permission**: this mirror never pushes. Note the expiry date; when the token expires the mirror goes to `error` until you replace it.
3. **Create the mirror.** In the web UI: **New repository → Import from URL**, fill in `https://github.com/<org>/GitStack.git`, choose **Keep in sync** (not *Copy once*), set the interval and, for a private origin, the GitHub user name and the token. The repository appears immediately (empty) and the first copy starts in the background.

   The same through the API (the token is only sent in this request):

   ```bash
   curl -fsS -X POST "https://homehub.example/api/v1/repos/import" \
     -H "Authorization: Bearer $GITSTACK_TOKEN" -H "Content-Type: application/json" \
     -d '{"owner":"<org>","name":"gitstack","visibility":"private",
          "url":"https://github.com/<org>/GitStack.git","mode":"mirror","intervalSeconds":3600}'
   # private origin: add  "username":"<github user>","token":"'"$GITHUB_TOKEN"'"
   ```

   On an **existing, empty** repository use **Settings → Pull mirror → Enable** (mockup P3–P6) or `POST /api/v1/repos/{owner}/{repo}/pull-mirror` with `{"url": …, "username": …, "token": …, "intervalSeconds": …}`.
4. **Interval.** For a backup copy use **1 hour** (`intervalSeconds: 3600`): it is plenty for a backup and gentle with GitHub. The default is **10 minutes**; the allowed range is 5 minutes to 24 hours. **Sync now** (`POST /api/v1/repos/{owner}/{repo}/pull-mirror/sync`, answers `202`) updates immediately and does not change the interval.
5. **Check it.** The mirror is healthy when its state is `in_sync` and `lastSyncedAt` is recent (`GET /api/v1/repos/{owner}/{repo}/pull-mirror`; the run log is `GET …/pull-mirror/runs`). Compare the tips:

   ```bash
   git ls-remote https://github.com/<org>/GitStack.git refs/heads/main
   git ls-remote https://homehub.example/<org>/gitstack.git refs/heads/main   # same SHA
   ```

### States

| State | Meaning |
|---|---|
| `pending` | Queued: first copy, or next round due. |
| `syncing` | A fetch is running (one at a time per repository, with a timeout). |
| `in_sync` | The last update succeeded. Refs listed in `divergedRefs` are kept as they were; the others are up to date. |
| `error` | The last attempt failed. It retries after 30 s, 2 min, 10 min, 1 h, 3 h, then every 6 h. A destination blocked by the egress policy is **not** retried: fix the policy (or the origin) and press **Sync now**. |
| `paused` | Updates are stopped by an admin; the repository stays read-only. **Resume** (`POST …/pull-mirror/resume`) brings it back. |
| `stopped` | Interrupted: the repository is normal and writable, credentials are deleted, the run log and the `diverged` refs are kept. |

The admins are also notified when there has been no successful update for more than 24 hours. The run log keeps the last 50 runs, for 30 days.

### When a ref is `diverged`

It means the origin rewrote or deleted a ref that GitStack already had. Nothing was lost: GitStack still has the old version, shown in **Settings → Pull mirror → Diverged refs** (`divergedRefs` in the API, with `localSha` and `remoteSha`). Decide which side is right. If the origin is right and you want its version, **Stop** the mirror, fix the ref in the now-writable repository, and enable the mirror again later; a mirror never accepts a rewrite on its own.

### Stop, or copy once

- **Stop** (**Settings → Pull mirror → Stop**, `POST …/pull-mirror/stop`): type the repository name to confirm (`{"confirm":"gitstack"}`). The repository becomes a normal one, credentials are deleted. This is how a copy becomes the new home after a migration. It can be enabled again later.
- **Copy once** (**Import from URL → Copy once**, or `"mode":"once"` in `POST /api/v1/repos/import`) is "create the mirror, sync, stop" in one step: all branches and tags are copied once, then the credentials are deleted and the repository is normal and writable, with no link to the origin. If the copy fails the repository stays with the error visible and you can retry with **Sync now**.

The pull mirror is proved end to end by `deploy/test-vm/e2e.ps1` (step e10) and by the stack test `TestPullMirrorGiroCompleto`.

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

Database
  Postgres ESTERNO             db.example.com:5432/gitstack (utente gitstack, sslmode verify-full)
  Versione PostgreSQL          16.4

Ultimo backup: ultimo successo: 2026-10-06 15:46:27 (/var/backups/gitstack/gitstack-backup-20261006T154627Z-sha-20636805d1756798054dd0b7c570ae0a3d4cc6ee.tar.gz.enc)

Stato: sano
```

With S3 backup configured, a line `Backup su S3: ...` shows the result of the
last upload (`ultimo successo` or `ULTIMO CARICAMENTO FALLITO` with the cause).
The overall health line does not depend on it: a failed upload is shown, and
the backup service exits with code `10`.

**JSON mode** (`--json`): produces structured output for scripting:

```sh
sudo gitstack status --json
```

The command reads `/etc/gitstack/config.yaml` (mode `0600`); run it
under `sudo` or with `GITSTACK_CONFIG` pointing to an accessible file.

With the bundled PostgreSQL the Database section reads
`Postgres interno (bundle)` and the server version. With an external database
`status` shows host, database, user, `sslmode` and the version read live from
the cluster (the one detected at install time, marked as such, if the cluster
cannot be queried). If the database cannot be queried from the `pgclient` pod,
the overall state is not healthy.

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

### CI tests of install, upgrade and restore (N2, V1)

The workflow `.github/workflows/install.yml` proves on a real runner what
used to be tried only on the board's VM. It does **not** run on every push
or pull request (each job installs k3s and every service and takes a long time):

| Trigger | When |
|---|---|
| `push` of a `v*` tag | at every release |
| `schedule` | every night at 02:30 UTC, on `main` |
| `workflow_dispatch` | by hand, on any branch (inputs `previous_commit` and `k3s_from` are optional) |

It runs two scenarios on `ubuntu-22.04` and `ubuntu-24.04`:

- **fresh**: `install.sh` of the commit under test → `gitstack backup`
  (encrypted) → more data → `gitstack restore` → the data written after the
  backup is gone.
- **upgrade**: `install.sh` of the **previous commit** (latest green CI run on
  `main`, strict ancestor, with its images and binary published) with a k3s
  **one patch older** than `deploy/k3s-version` → data → `gitstack upgrade`
  (preventive backup → k3s patch → GitStack, N10) → k3s checked with
  `k3s --version`, `kubectl version` and `gitstack status --json`
  (`k3s_version`) → backup → more data → restore.

Both check health, the admin login with the forced password change, user and
token creation, repository creation, `git clone` and `git push` over HTTPS.
The steps are in `deploy/ci/install-e2e.sh` (shellcheck in `ci.yml`).

**Signatures (V5).** `install.sh` and `gitstack upgrade` verify the cosign
signatures of the binary, the chart and the images, so the run tests that
verification too. The job `release` decides where the artefacts of the commit
come from:

| Run | Artefacts | Signatures |
|---|---|---|
| `schedule` (main) and `v*` tag | only the signed release `sha-<commit>` and its images, published by `ci.yml` | **verified**. If the `.sigstore.json` bundles are not there yet (on a tag push `ci.yml` and this workflow start together) the job waits for them up to 75 minutes, then fails with an explicit error; it never builds artefacts itself and never passes the exception |
| `workflow_dispatch` on a commit whose release is already signed | that release | **verified** |
| `workflow_dispatch` on a branch or an unsigned commit | built by the jobs `images` and `admin-binary` | **not verified: exception N9**, passed explicitly as `--allow-unsigned-mirrors` to `install.sh` and to `gitstack upgrade` (only if the installed `gitstack` of the previous commit knows the flag; an older one verifies nothing), and written in the run summary (`GITHUB_STEP_SUMMARY`) |

In the upgrade scenario the previous commit is installed with its own
`install.sh` and its own signed artefacts, without the exception; then the
installed `gitstack` (which already verifies, from GIT-209 on) checks the
artefacts of the commit under test. The exception is never silent, and never
on the runs of `main` and of the tags.

The jobs `images` and `admin-binary` therefore run only in the last case: they
publish the images (`ghcr.io`) and the `gitstack` binary (release
`sha-<commit>`) with the same tags, because `gitstack upgrade` always downloads
from GitHub, there is no local source for it. To try a change, push the branch
and run the workflow by hand on it:

```sh
gh workflow run install.yml --ref my-branch
gh run watch
```

Runners start with about 14 GB free: the installer is called with
`--skip-preflight` (the hardware profile is covered by
`deploy/tests/preflight_test.sh`).

#### Pop!_OS 22.04 and 24.04

Not covered in CI: GitHub hosts no Pop!_OS runner, and System76 does not
publish a cloud image or a container with systemd that stands for the real
system (to be verified again before deciding). What CI gives instead: Pop!_OS
22.04 and 24.04 are Ubuntu 22.04 and 24.04 with the System76 packages, so the
same installer path runs on the same base; the `ID=pop` detection is covered
by `deploy/tests/preflight_test.sh`. The real run on Pop!_OS stays manual,
on the test VM (`deploy/test-vm/`). Options, from the cheapest, for the CEO
to choose:

1. **Keep as is** (declared limit): Ubuntu in CI, Pop!_OS by hand before each
   release, noted in the release checklist.
2. **Self-hosted runner** on a Pop!_OS machine of the company (the board's
   homehub): exact, but a permanent machine and a runner open to the repo.
3. **KVM VM on the runner** with the Pop!_OS ISO: the installer of the ISO is
   interactive, so it needs a preseeded image built and kept by us; the most
   work, and the result is still a copy of the real system.

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
| `gitstack restore --from s3 [NAME]` | yes | Download from S3 (newest, or NAME) and restore |
| `gitstack config set backup.s3 --endpoint E --bucket B --key-file F` | yes | Configure the encrypted S3 copy of backups |
| `gitstack upgrade` | yes | Upgrade with preventive backup and automatic rollback |
| `gitstack upgrade --dry-run` | yes | Check readiness without making changes |
| `gitstack config set host NEWNAME` | yes | Change the host name (certificates, services, config) |
| `gitstack config set network [options]` | yes | Change proxy, registry mirror, download mirror and corporate CA (see [Corporate network](#corporate-network-proxy-mirrors-and-ca)) |

\* `gitstack status` reads `/etc/gitstack/config.yaml` (mode `0600`),
so it fails as a non-root user with a permission error.

### Common flags

| Flag | Applies to | Purpose |
|---|---|---|
| `--config PATH` | All commands | Override config file path (default: `/etc/gitstack/config.yaml`) |
| `--dest DIR` | `backup`, `restore`, `upgrade` | Override the backup destination directory |
| `--key-file FILE` | `backup`, `restore`, `upgrade` | Path to the AES-256-GCM encryption key |
| `--to TAG\|SHA` | `upgrade` | Target version; default is latest on the `main` branch |
| `--version vX.Y.Z` | `upgrade` | Target released version (SemVer tag only), signed release `vX.Y.Z` |
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
| `9` | Data restored but the instance does not answer through the Ingress |
| `10` | Local backup done, upload to S3 failed (the local archive is valid) |
| `11` | `gitstack postgres check`: external PostgreSQL not suitable |
| `70` | Unexpected error (backup failure, network, unrecoverable) |

### Configuration files

| Path | Purpose |
|---|---|
| `/etc/gitstack/config.yaml` | Main configuration (mode `0600`) |
| `/etc/gitstack/tls/` | CA certificate, CA key, server certificate (internal CA mode) |
| `/etc/gitstack/network/ca.crt` | Corporate CA copy (only with `--ca-bundle`) |
| `/etc/rancher/k3s/registries.yaml` | k3s registry mirror (mode `0600`, may hold the mirror password) |
| `/etc/systemd/system/k3s.service.d/10-gitstack-network.conf` | Proxy for k3s and containerd |
| `deploy/postgres-prepare.sql` | Script for the DBA: database and user for an external PostgreSQL |
| `/etc/gitstack/backup-state.json` | Last backup status written by `gitstack backup` |

---

## V5 — Catena di fornitura: firme e SBOM

GitStack firma le immagini container, il chart Helm e i binari della CLI `gs`
con Sigstore/cosign (firma keyless tramite OIDC di GitHub Actions). Gli SBOM
sono generati con syft (formato SPDX).

### Come verificare una firma

#### Prerequisiti

```sh
# Installa cosign (Sigstore) e syft (anchore)
# macOS (Homebrew):
brew install sigstore/cosign/cosign
brew install anchore/syft/syft
# Linux: scarica dal sito ufficiale di Sigstore e anchore.
```

#### Identità e issuer attesi

| Parametro | Valore |
|---|---|
| Identity del certificato (main) | `https://github.com/fathorMB/GitStack/.github/workflows/ci.yml@refs/heads/main` |
| Identity del certificato (tag v*) | `^https://github.com/fathorMB/GitStack/\.github/workflows/ci\.yml@refs/tags/v.*$` |
| OIDC Issuer | `https://token.actions.githubusercontent.com` |

> **Nota:** l'identity è l'URL del workflow GitHub Actions (`github.com/<owner>/<repo>/.github/workflows/ci.yml@refs/heads/main`), **non** l'issuer. L'issuer è `https://token.actions.githubusercontent.com`.

#### Verifica di un'immagine container

```sh
# Immagine: ghcr.io/<owner>/gitstack-gateway:sha-<commit-sha>
IMG="ghcr.io/fathormb/gitstack-gateway:sha-<commit-sha>"

# Verifica la firma keyless: identity (URL del workflow) + issuer OIDC.
cosign verify \
  --certificate-identity "https://github.com/fathorMB/GitStack/.github/workflows/ci.yml@refs/heads/main" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  "${IMG}"
```

Per i tag, usare `--certificate-identity-regexp` con:
`^https://github.com/fathorMB/GitStack/\.github/workflows/ci\.yml@refs/tags/v.*$`

#### Verifica di un binario

```sh
# Scarica il binario e il suo bundle dalla release GitHub.
# La release si chiama "sha-<commit-sha>" e contiene i file con estensione .sigstore.json.

curl -fsSL -o gitstack-linux-amd64 \
  "https://github.com/fathorMB/GitStack/releases/download/sha-<commit-sha>/gitstack-linux-amd64"
curl -fsSL -o gitstack-linux-amd64.sigstore.json \
  "https://github.com/fathorMB/GitStack/releases/download/sha-<commit-sha>/gitstack-linux-amd64.sigstore.json"

# Verifica la firma del blob: usa --bundle con il file .sigstore.json.
cosign verify-blob \
  --certificate-identity "https://github.com/fathorMB/GitStack/.github/workflows/ci.yml@refs/heads/main" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --bundle gitstack-linux-amd64.sigstore.json \
  gitstack-linux-amd64
```

#### Verifica del chart Helm

```sh
# Scarica il chart firmato dalla release.
curl -fsSL -o gitstack-0.1.0.tgz \
  "https://github.com/fathorMB/GitStack/releases/download/sha-<commit-sha>/gitstack-0.1.0.tgz"
curl -fsSL -o gitstack-0.1.0.tgz.sigstore.json \
  "https://github.com/fathorMB/GitStack/releases/download/sha-<commit-sha>/gitstack-0.1.0.tgz.sigstore.json"

# Verifica la firma del blob chart: usa --bundle con il file .sigstore.json.
cosign verify-blob \
  --certificate-identity "https://github.com/fathorMB/GitStack/.github/workflows/ci.yml@refs/heads/main" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --bundle gitstack-0.1.0.tgz.sigstore.json \
  gitstack-0.1.0.tgz
```

### Verifica degli SBOM

Gli SBOM (Software Bill of Materials) in formato SPDX sono caricati nella release
GitHub e disponibili come artifact del workflow di firma.

```sh
# SBOM di un'immagine: scarica dalla release o dall'artifact del workflow.
curl -fsSL -o gitstack-gateway-sbom.spdx.json \
  "https://github.com/fathorMB/GitStack/releases/download/sha-<commit-sha>/gitstack-gateway-sbom.spdx.json"

# Controlla il contenuto: elencare i pacchetti rilevati.
syft gitstack-gateway-sbom.spdx.json --quiet -o table

# SBOM degli altri componenti (tutti nella release):
# - gitstack-identity-sbom.spdx.json
# - gitstack-git-sbom.spdx.json
# - gitstack-core-sbom.spdx.json
# - gitstack-web-sbom.spdx.json
# - gitstack-chart-sbom.spdx.json
# - gitstack-binary-sbom.spdx.json
# - gs-binary-sbom.spdx.json
```

### Attestazioni delle immagini

Ogni immagine ha un'attestazione che collega l'SBOM all'immagine stessa
(standard Sigstore in-toto). Si verifica con `cosign verify-attestation`:

```sh
# Verifica l'attestazione di un'immagine.
cosign verify-attestation \
  --certificate-identity "https://github.com/fathorMB/GitStack/.github/workflows/ci.yml@refs/heads/main" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --type spdxjson \
  ghcr.io/fathormb/gitstack-gateway:sha-<commit-sha>
```

Per i tag, usare `--certificate-identity-regexp` con:
`^https://github.com/fathorMB/GitStack/\.github/workflows/ci\.yml@refs/tags/v.*$`

### File nella release GitHub

La release `sha-<commit-sha>` contiene:

| Tipo | File |
|---|---|
| Binari | `gitstack-linux-amd64`, `gitstack-linux-amd64.sha256`, `gs_linux_amd64`, … |
| Firme blob | `*.sigstore.json` (uno per binario/chart/checksum) |
| SBOM immagini | `gitstack-gateway-sbom.spdx.json`, `gitstack-identity-sbom.spdx.json`, `gitstack-git-sbom.spdx.json`, `gitstack-core-sbom.spdx.json`, `gitstack-web-sbom.spdx.json` |
| SBOM chart | `gitstack-chart-sbom.spdx.json` |
| SBOM binario | `gitstack-binary-sbom.spdx.json`, `gs-binary-sbom.spdx.json` |
| Chart | `gitstack-<versione>.tgz`, `gitstack-<versione>.tgz.sigstore.json` |

### Flusso di firma

1. I job `registry`, `admin-binary` e `gs-binaries` pubblicano immagini, chart e
   binari nella release GitHub (`sha-<commit-sha>`).
2. Il job `sign` (dipende da tutti e tre) scarica gli asset, genera gli SBOM
   con syft e firma con cosign:
   - Immagini: `cosign attest --type spdxjson` (SBOM come attestazione in-toto)
     + `cosign sign` (firma keyless OIDC).
   - Chart: `cosign sign-blob --bundle` (firma del file .tgz, output `.sigstore.json`).
   - Binari: `cosign sign-blob --bundle` (firma di ogni binary/checksum).
3. Firme (.sigstore.json), SBOM (.spdx.json) e file originali vengono caricati
   nella release GitHub con `gh release upload --clobber`.
4. Step di verifica interna: `cosign verify` su immagine,
   `cosign verify-blob` su chart e binario, tutti senza `|| true`.

### Rilascio con versione (`vX.Y.Z`, GIT-222)

Oltre alle build `sha-<commit>` di ogni push su `main`, un tag Git `vX.Y.Z` o
`vX.Y.Z-rc.N` (SemVer con prefisso `v`) produce una **GitHub Release con il nome
della versione**. Lo fa il job `release` di `ci.yml`, dopo `registry`,
`admin-binary`, `gs-binaries` e `sign`: le immagini sono quelle di `sha-<commit>`
(stesso digest, quindi stesse firme), con in più i tag `vX.Y.Z` e `X.Y.Z`.

La release `vX.Y.Z` contiene:

| Tipo | File |
|---|---|
| Binari | `gitstack-linux-amd64` e `.sha256`, `gs_<os>_<arch>[.exe]` (6 piattaforme), `gs-skills.zip`: costruiti con la versione `vX.Y.Z` dentro il binario |
| Chart | `gitstack-X.Y.Z.tgz`: `version` e `appVersion` = `X.Y.Z`, quindi il tag immagine di default è `X.Y.Z` |
| Firme | `<file>.sigstore.json` per binari, chart e `SHA256SUMS` (cosign keyless, identità `ci.yml@refs/tags/v*`) |
| SBOM | `gitstack-<servizio>-sbom.spdx.json` (5 immagini), `gitstack-chart-sbom.spdx.json`, `gitstack-binary-sbom.spdx.json`, `gs-binary-sbom.spdx.json` |
| Checksum | `SHA256SUMS`: tutti i file sopra, firmato |
| Changelog | `CHANGELOG-vX.Y.Z.md` (la sezione, anche nelle note della release) e `CHANGELOG.md` aggiornato |

Una versione con prerelease (`-rc.N`, `-test.N`) è marcata «pre-release» e non è la
«latest». Il job rifiuta un tag che non è SemVer.

**Changelog.** `scripts/release-changelog.sh <tag>` elenca i commit fra il tag e il
precedente (l'ultimo `v*` raggiungibile, senza i tag `v*-test.*` per un rilascio
vero), raggruppati per tipo di conventional commit. La sezione va nelle note e in
`CHANGELOG.md`: il job non scrive su `main`, apre una PR `changelog/vX.Y.Z` che il
board approva. In locale: `scripts/release-changelog.sh v1.0.0 --prepend CHANGELOG.md`.

**Installare una versione.**

```sh
# Senza checkout (curl | bash): chart, binario e immagini della versione, firme verificate
curl -fsSL https://raw.githubusercontent.com/fathorMB/GitStack/vX.Y.Z/deploy/install.sh \
  | sudo bash -s -- --version vX.Y.Z --host homehub.local

# Aggiornare un'installazione esistente (backup preventivo, verifica, rollback)
sudo gitstack upgrade --version vX.Y.Z --dry-run
sudo gitstack upgrade --version vX.Y.Z
```

`--version` fissa insieme il sorgente (ref), il tag immagine e la release da cui
si scaricano binario e chart. Vale lo stesso trattamento di `sha-<commit>`: bundle
obbligatorio, identità del workflow `ci.yml` (da `main` o da un tag `v*`) e issuer
fissati, cosign pinnato, immagini verificate prima di `helm upgrade`. Il chart
scaricato deve avere `version` e `appVersion` uguali alla versione richiesta, e
`--version` non si combina con un `--image-tag` diverso. Un valore che non è
`vX.Y.Z[-prerelease]` è rifiutato: per un commit si usa `--image-tag sha-<commit>`
(install) o `--to sha-<commit>` (upgrade). Con `--download-mirror` la release si
pubblica in `<mirror>/gitstack/vX.Y.Z/` (stessa struttura di `sha-<commit>`), il
sorgente in `<mirror>/gitstack/source/vX.Y.Z.sha`.

**Provare la pipeline.** Mai su un tag `v1`: si usa un tag di prova
`v0.0.0-test.N` su un fork o un repository di prova (`git tag v0.0.0-test.1 &&
git push <fork> v0.0.0-test.1`). Il tag `v1.0.0-rc.1` lo decide il board.

### Verifica automatica in `install.sh` e `gitstack upgrade` (GIT-209)

Binario, chart e immagini si verificano da soli, a ogni installazione e a ogni
aggiornamento, con le stesse regole della CI. Se una firma manca o non torna,
l'operazione si ferma **prima di toccare il sistema** con un messaggio che dice
quale artefatto è in causa.

| Artefatto | Come si verifica |
|---|---|
| Binario `gitstack` | SHA-256 (`.sha256` della release o `--admin-sha256`) **e** `cosign verify-blob` col bundle `gitstack-linux-amd64.sigstore.json` scaricato accanto al binario |
| Chart Helm | si scarica il `.tgz` pubblicato dalla CI (`gitstack-<versione>.tgz` nella release `sha-<commit>`) con il suo `.sigstore.json`, si verifica e solo dopo si estrae: il sorgente di codeload non ha i byte firmati e non si usa |
| Immagini `gitstack-gateway/identity/git/core/web` | `cosign verify` sull'immagine che il chart distribuirebbe (stessi `--set`/`-f` dell'installazione), prima di `helm upgrade --install`. Postgres e NATS sono immagini di terzi: non le firma GitStack |

- **Identità fissata**: il workflow `ci.yml` del repository (`GITSTACK_REPO`,
  default `fathorMB/GitStack`) da `main` o da un tag `v*`, regexp
  `^https://github\.com/<repo>/\.github/workflows/ci\.yml@refs/(heads/main|tags/v.+)$`;
  issuer `https://token.actions.githubusercontent.com`. Nessun flag che
  salti il controllo del registro di trasparenza.
- **Bundle obbligatorio**: senza `.sigstore.json` l'artefatto «non risulta
  firmato» ed è rifiutato, mai accettato. Una release `sha-<commit>` ha i bundle
  solo dopo che il job `sign` della CI è finito.
- **cosign pinnato come Helm**: `install.sh` installa cosign `v2.4.0` (la
  versione della CI) in `/usr/local/bin/gitstack-cosign` verificandone lo
  SHA-256 pinnato; non usa un cosign trovato nel `PATH`. `gitstack upgrade` usa
  lo stesso binario (`v5.cosign_path` in `/etc/gitstack/config.yaml`). In una
  rete chiusa: `GITSTACK_COSIGN_URL` (percorso o URL del mirror interno di
  cosign) e `GITSTACK_COSIGN_SHA256`; il checksum resta verificato.
- **Chart da cartella locale** (`--chart-dir` o checkout): non è un artefatto
  firmato e non si può verificare; `install.sh` lo dice con un avviso, e le
  immagini si verificano comunque.

#### Eccezione per mirror interni non firmati (N9)

Per un mirror interno che copia release e immagini senza le firme esiste
un'eccezione **esplicita**, mai il default:

```sh
sudo ./deploy/install.sh --allow-unsigned-mirrors     # installazione
sudo gitstack upgrade --allow-unsigned-mirrors        # aggiornamento, solo per questa volta
```

- Salta **tutte** le verifiche cosign (binario, chart, immagini); il checksum
  SHA-256 del binario resta obbligatorio.
- Emette un avviso `ECCEZIONE N9 ATTIVA` a ogni passo saltato e nel riepilogo
  finale.
- Con l'installer l'eccezione è **salvata** in `/etc/gitstack/config.yaml`
  (`v5.allow_unsigned: true`): `gitstack upgrade` la rispetta e
  `gitstack status` mostra «AVVISO catena di fornitura» (campo
  `supply_chain_warning` con `--json`). Non rende «non sano» lo stato: è un
  avviso, non un guasto.
- Rieseguire `install.sh` **senza** il flag la rimuove e ripristina la
  verifica.
- La pagina Admin · System (mockup 16) non esiste ancora nel web: finché non
  c'è, l'avviso si vede in `gitstack status`; la pagina leggerà lo stesso
  report.

*V5 — Catena di fornitura (GIT-208, GIT-209)*

---

*Guida operativa — install.sh (T-09 di M-08 [c_8458909a21d9035f]),
decisioni D17–D19 [c_1d1d61aca3dea601]*
