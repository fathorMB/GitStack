# Operations Guide — GitStack

> **Scope:** This guide covers installation, backup, restore, upgrade, push
> mirrors, and diagnostics of a GitStack instance deployed on a single Linux host with k3s.
> It is written for operators who manage the machine directly (for example the
> `homehub` appliance described in M-08 [c_8458909a21d9035f]).
>
> **Supported systems (N2, revised 2026-10-18):** officially only Ubuntu Server
> 22.04 and 24.04 LTS (amd64), tested in CI. Pop!_OS 22.04 and 24.04 installs
> without `--force`, but with a warning in the preflight: not officially
> supported in v1.0, no guarantee and no CI test. Other systems require
> `--force`.

## Installation

### Prerequisites

GitStack runs on a clean machine with one of the following systems (all amd64):

- Ubuntu Server 22.04 LTS — officially supported in v1.0, tested in CI
- Ubuntu Server 24.04 LTS — officially supported in v1.0, tested in CI
- Pop!_OS 22.04 — installs without `--force`, with a warning in the preflight (not officially supported in v1.0)
- Pop!_OS 24.04 — installs without `--force`, with a warning in the preflight (not officially supported in v1.0)

Other distributions and WSL2 are not supported: the preflight stops there, and only `--force` proceeds, at your own risk.

| Profile | CPU | RAM | Disk |
|---|---|---|---|
| **Minimum** (blocking) | 4 vCPU | 8 GB | 60 GB (≥ 20 GB free on the filesystem that holds `/var/lib/rancher`) |
| **Recommended** (advisory) | 8 vCPU | 16 GB | 200 GB SSD (≈ 100 users) |

The sizing figures above (and the "≈ 100 users" of the recommended profile) are
**indicative, not measured**: no capacity benchmark backs them. The only load
test is a smoke test, see [Load smoke test (V3)](#load-smoke-test-v3).

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
| `--egress-allow LIST` | `GITSTACK_EGRESS_ALLOW` | — | LAN ranges (IP, CIDR or names, comma-separated) that webhooks and mirrors may reach (`egress.allow`). If set, only these destinations are allowed. See [Egress policy and the LAN](#egress-policy-and-the-lan). |
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

#### Apps domain and wildcard DNS (v3, H2)

Apps hosted by GitStack get an automatic address
`<app>-<repo>-<owner>.<apps-domain>` (for example
`web-handbook-acme.apps.acme.it`, ADR 0002). The
domain is chosen at install time with `--apps-domain` (optional). Without it
the installer uses `apps.<host>` (`apps.gitstack.internal` when `--host` is an
IP) and prints a warning. A domain separate from GitStack's own
(`git.example.com` and `apps.example.com`) is recommended; a subdomain of it
(`apps.git.example.com`) is allowed; the same name as the host is refused. The
name is lowercase DNS, at least two labels, letters, digits and `-`.

**DNS to configure.** Create a wildcard record that points to GitStack's IP,
before users open an app:

```text
*.apps.example.com.   300  IN  A  192.168.1.20
```

(dnsmasq: `address=/apps.example.com/192.168.1.20`.) A `.local` domain cannot
work: mDNS has no wildcard records. The installer and
`gitstack config set apps-domain` check `getent hosts
gitstack-dns-check.<apps-domain>` and warn, without stopping, if it does not
resolve to this machine.

**Certificate.** With the internal CA, `gitstack-tls` adds `*.<apps-domain>`
to the SANs of the server certificate: same CA, same nightly renewal, no extra
CA on the clients. A wildcard covers one label (`web-handbook-acme.apps.acme.it`,
not `a.b.apps.acme.it`). With your own certificate (`--tls-cert`) it must
already cover `*.<apps-domain>`; `gitstack-tls` warns if it does not. With
`--tls letsencrypt` the address is recorded but the internal-CA wildcard is
not issued. Check: `openssl x509 -in /etc/gitstack/tls/server.crt -noout -ext
subjectAltName`.

**Change it later, and upgrade from v2.** `sudo gitstack config set
apps-domain apps.example.com` reissues the certificate (same CA) and rewrites
`apps_domain:` in `config.yaml`. An installation upgraded from v2 has no apps
domain (`gitstack status` prints "Dominio delle app: non impostato"); set it
with this command. Apps already created keep their address.

**Collisions.** `<app>-<repo>-<owner>` is a single DNS label (at most 63
characters): owner and repository are lowercased and `_`, `.` and other
characters become `-`. The owner in the label keeps same-named repositories of
different owners apart. There are no automatic suffixes: a label over 63
characters is refused with `address_too_long`, and one already in use (`a-b`
in `c` against `a` in `b-c`, same owner) with `address_conflict`, enforced by
the unique index on `apps.normalized_host` (`pkg/names`: `AppAddress`,
`CheckAppAddress`).

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

### Egress policy and the LAN

Webhooks and mirrors call addresses given by users, so every outgoing connection goes through the egress policy (`pkg/egress`, values `egress.*`). Always blocked, whatever the lists say: loopback, link-local (cloud metadata), the cluster networks (`egress.clusterCIDRs`), **the node's own addresses** and **the Kubernetes ports 6443, 10250, 10255 and 2379-2380 on any private address**. This keeps a repository admin from making core call kubelet, the k3s API or a service listening on the host.

The node's addresses are `status.hostIP` of the pod (`GITSTACK_NODE_IP`), the IPv4 addresses `install.sh` finds on the host (`egress.nodeAddresses`) and any address you add to `egress.nodeAddresses`. A host with other addresses (a second NIC, a VIP) should list them there.

Other hosts of your LAN stay reachable by default, because CI servers and Git hosts usually live there. To close the list, state the networks webhooks and mirrors may reach:

```bash
sudo ./install.sh --egress-allow 192.168.1.0/24,ci.example.com
```

Entries are IPs, CIDRs, names (`hooks.example.com`) or suffixes (`*.example.com`). With a non-empty list, only what it covers is allowed (public addresses included). It is written in `egress.allow`, and like any `--set` it must be given again at every upgrade. A blocked delivery fails at once, without a retry, and the delivery log shows no response.

### API rate limiting (F6)

The gateway limits `/v1/*` calls with an in-memory token bucket, on two keys: the **credential** (personal token `gst_...` or `gst_session` cookie; the key is its SHA-256, the value is never logged) and the **client IP**. The IP is the real client one: `X-Forwarded-For` counts only when the connection comes from a proxy in `gateway.env.trustedProxies` (default `10.42.0.0/16`, Traefik on k3s); from any other peer it is ignored. Health probes (`/healthz`, `/readyz`) are not limited.

| Value | Env of the gateway | Default | Meaning |
|---|---|---|---|
| `gateway.env.rateLimit.credentialRps` | `GITSTACK_GATEWAY_RATE_LIMIT_CREDENTIAL_RPS` | `20` | Requests per second per credential at steady state; `0` disables the per-credential limit |
| `gateway.env.rateLimit.credentialBurst` | `GITSTACK_GATEWAY_RATE_LIMIT_CREDENTIAL_BURST` | `200` | Burst per credential (at least 1) |
| `gateway.env.rateLimit.ipRps` | `GITSTACK_GATEWAY_RATE_LIMIT_IP_RPS` | `50` | Requests per second per client IP; `0` disables the per-IP limit |
| `gateway.env.rateLimit.ipBurst` | `GITSTACK_GATEWAY_RATE_LIMIT_IP_BURST` | `500` | Burst per IP (at least 1) |

The per-IP threshold is wider because several users can share one address behind a NAT. Change it with `--set gateway.env.rateLimit.ipRps=100` (like any `--set`, it must be given again at every upgrade).

Over the threshold the gateway answers `429` with `Retry-After` (seconds) and the error code `rate_limited`. Every rejection is counted; the gateway logs it at `warn` with the message `rate limit exceeded` and the fields `scope` (`credential` or `ip`), `key` (credential hash prefix or IP), `rejected_total` and `suppressed_since_last_log` (at most one line per second and scope, so a flood does not flood the logs). To alert on abnormal volumes, match that message in your log pipeline (for example `kubectl logs deploy/<release>-gateway | grep 'rate limit exceeded'`).

Residual limits:

- Counters live in the memory of each gateway replica: with N replicas the effective ceiling is up to N times the value, and a restart resets them. Up to 100000 keys per scope are tracked; beyond that, new keys are not limited rather than exhausting memory.
- The **Git endpoints** (clone, fetch and push over HTTPS on the Git ingress, and SSH) are served by the git service outside the gateway and are **not** covered by this limiter. Only the `/v1/*` API is. Throttling of Git traffic must come from the ingress (Traefik middleware) or the network; the login limiter of identity (`LOGIN_MAX_ATTEMPTS_*`) is separate and unchanged.
- Credential guessing is not what the limiter is for (tokens have 256 bits): it bounds scraping and denial of service by a single credential or address.

### Security logging (F10)

Identity and the gateway write security events as one JSON line each on stdout, with the field `event` (`security.*`) so they can be selected without parsing messages. Passwords, tokens, cookies and the `Authorization` header are never logged: events carry only identifiers (user id, username, token id, key fingerprint). The `service` field says which service wrote the line.

| `event` | Service | When | Useful fields |
|---|---|---|---|
| `security.login.success` | identity | password or OIDC login accepted | `actor`, `actor_id`, `method`, `session_id`, `client_ip`, `request_id` |
| `security.login.failure` | identity | wrong credentials or failed OIDC login (`warn`) | `username`, `reason`, `client_ip`, `request_id` |
| `security.login.rate_limited` | identity | login throttled after repeated failures (`warn`) | `username`, `client_ip` |
| `security.logout` | identity | session revoked by the user | `actor`, `session_id` |
| `security.password.changed`, `security.password.change_failed`, `security.password.change_rate_limited` | identity | password change, wrong current password (`warn`), throttled (`warn`) | `actor`, `target` |
| `security.token.created`, `security.token.revoked` | identity | personal or agent token created/revoked | `created_token_id` / `revoked_token_id`, `token_name`, `scopes`, `target` |
| `security.ssh_key.added`, `security.ssh_key.deleted` | identity | SSH key changes | `key_id`, `fingerprint` |
| `security.grant.created`, `security.grant.updated`, `security.grant.deleted` | identity | resource grants | `resource_id`, `grant_id`, `subject_type`, `subject_id`, `role` |
| `security.user.created`, `security.user.deactivated`, `security.user.activated`, `security.user.admin_changed`, `security.user.deleted` | identity | account lifecycle by an administrator | `actor`, `target`, `target_id` |
| `security.panic` | gateway | a handler panicked: the client got a generic `500`, the line has `panic` and `stack` (`error`) | `request_id`, `method`, `path` |

The gateway request line (message `richiesta gestita`) has `client_ip` (the real client: `X-Forwarded-For` counts only from `gateway.env.trustedProxies`, `remote_addr` stays the peer), `user_id`, `username` and `token_id` when the request was authenticated, and a `reason` for `401`, `403`, `429` and `500` (`missing_credential`, `credential_kind_not_allowed`, `credential_inactive`, `credential_too_long`, `insufficient_scope`, `password_change_required`, `permission_denied`, `identity_unavailable`, `rate_limited`, `panic`). The `request_id` is the same in the gateway line and in the identity events of that request.

**Shipping the logs.** Logs stay on the node (`kubectl logs`, container runtime files under `/var/log/pods`). To keep them off the machine, run a collector on the node and send the JSON lines to your log store. Example with promtail (Loki), reading the pod files and selecting only the GitStack namespace:

```yaml
scrape_configs:
  - job_name: gitstack
    kubernetes_sd_configs: [{role: pod}]
    relabel_configs:
      - source_labels: [__meta_kubernetes_namespace]
        regex: gitstack
        action: keep
      - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_name]
        target_label: app
    pipeline_stages:
      - cri: {}
      - json: {expressions: {event: event, service: service, level: level}}
      - labels: {event: , service: , level: }
```

With vector the equivalent is a `kubernetes_logs` source, a `remap` that does `. = merge!(., parse_json!(.message))`, and your sink. Use the namespace you installed into.

**What to alert on** (suggested thresholds, tune to your size):

- `event="security.login.failure"` more than 10 per 5 minutes from one `client_ip`, or any `security.login.rate_limited`: password guessing.
- A `security.login.success` for a user right after several failures from the same `client_ip`.
- `security.token.created`, `security.grant.created`, `security.user.admin_changed` or `security.password.changed` outside working hours, or by an unexpected `actor`.
- Any `security.panic`: a bug reachable from the network; the `stack` is in the line.
- Gateway lines with `status` 403 and `reason="insufficient_scope"` or `permission_denied` repeated from one `user_id` or `token_id`: a credential probing what it may do.
- The gateway `rate limit exceeded` warning (see "API rate limiting") and a sustained `status` 5xx rate.

There is no separate audit table: the log stream is the audit trail, so keep it for as long as your policy requires. Timestamps are UTC, RFC 3339.

### Key lifecycle and rotation (F9, GIT-235)

GitStack has four long-lived keys. This section says who owns each one, where it lives, how it is rotated and what a rotation costs. All of them are Kubernetes Secrets with `helm.sh/resource-policy: keep`: `helm upgrade` and `helm uninstall` never regenerate them, and a backup (`gitstack backup`) contains them (`secrets.json`).

| Key | Secret and key | Held by | Used for | Rotation |
|---|---|---|---|---|
| Service secret of the gateway | `<release>-service-secrets`, `gateway` | gateway; verified by identity and core | Bearer to identity `/internal/*`; HMAC of the user identity forwarded to core | three phases below, no downtime |
| Service secret of core | `<release>-service-secrets`, `core` | core; verified by identity and git | Bearer to identity `/internal/*`; HMAC of the identity core sends to git | three phases below, no downtime |
| Service secret of git | `<release>-service-secrets`, `git` | git; verified by identity and core | Bearer to identity (`/internal/verify`, `permissions/check`, `ssh-keys`); HMAC of the identity git sends to core | three phases below, no downtime |
| OIDC key | `<release>-identity-oidc-key`, `key` (+ `identity.oidc.encKey.keyId`) | identity | AES-256-GCM of the OIDC client secrets in the database and of the login state cookie | new key and new `keyId`, one restart of identity |
| TOTP keyring | `<release>-identity-totp-key`, `keys` (+ `identity.totp.keys.keyId`) | identity | AES-256-GCM of the users' TOTP secrets (second factor, F11) | new key first in the list, old key after it, one restart of identity |
| Webhook keyring | `<release>-core-webhook-key`, `key` and `oldKeys` (+ `core.webhookSecret.keyId`) | core | AES-256-GCM of webhook and mirror secrets | new key, new `keyId`, old key in `oldKeys`, one restart of core |

Owner of all of them is the installation administrator (the operator who runs `gitstack`). Rotate a key when someone who could read the Secret leaves, when a pod or a backup may have leaked it, and at least once a year. After a suspected compromise of a single service, rotate that service's key first (see "Emergency").

#### Service secrets: one per caller

Gateway, core and git each have their own secret and the receiving service checks which one signed the request (`services/core/internal/trust/trust_test.go#TestVerify_SegretoDiUnAltroServizioRifiutato`):

- **identity** knows the three keys and uses them as Bearer on `/internal/*`. The gateway may call only `/internal/verify` and `/internal/permissions/check`; git also `/internal/ssh-keys/{fingerprint}`; core the rest. Anything else answers `403 forbidden` (`services/identity/router_test.go#TestBuildRouter_SegretiPerChiamante`). The identity header (`X-Gitstack-*`) is accepted from the gateway key only.
- **core** accepts the identity signed with the gateway key for every route, and the one signed with the git key only for `GET /repos/{owner}/{repo}`, which is what git needs to resolve `owner/repo` of a user it has already authenticated; every other call signed by git answers `403 forbidden` (`services/core/internal/httpserver/router_trust_test.go#TestRouter_GitPuoSoloRisolvereOwnerERepo`). Git, which parses untrusted repositories, cannot mint an identity for identity or for the rest of core.
- **git** knows its own key and the core key (to verify `/internal/*`); it never receives the gateway key.

Each Deployment receives only the variables it needs: `GITSTACK_SERVICE_SECRET_GATEWAY`, `GITSTACK_SERVICE_SECRET_CORE`, `GITSTACK_SERVICE_SECRET_GIT` (chart test `deploy/tests/chart_hardening_test.sh`, section "One service secret per caller"). The variable may hold a list `new,old`: the first key signs, every key verifies. That is the double key valid during a rotation (`services/core/internal/trust/trust_test.go#TestRotazione_DoppiaChiaveNellaFinestra`). A single key is the normal state.

The old shared variable `GITSTACK_IDENTITY_SERVICE_SECRET` is still read by the services as a fallback for a caller that has no key of its own (installations that do not use the chart); the chart never sets it. Do not use it in production: it brings back the shared secret.

#### Rotating a service secret (no downtime)

A rotation needs three phases because pods restart one at a time: a signer that already uses the new key must be verified by receivers that already know it. `$R` is the release name (default `gitstack`) and `$NS` its namespace.

```bash
R=gitstack; NS=default
K="sudo env KUBECONFIG=/etc/rancher/k3s/k3s.yaml k3s kubectl -n $NS"
KEY=git                                   # gateway | core | git
OLD=$($K get secret $R-service-secrets -o jsonpath="{.data.$KEY}" | base64 -d)
NEW=$(openssl rand -hex 20)               # 40 characters

set_key() { $K patch secret $R-service-secrets --type merge -p '{"stringData":{"'"$KEY"'":"'"$1"'"}}'; }
roll() {
  for d in identity core git gateway; do $K rollout restart deployment/$R-$d; done
  for d in identity core git gateway; do $K rollout status deployment/$R-$d --timeout=300s; done
}

set_key "$OLD,$NEW"; roll    # 1. every service accepts both; the signer still signs with the old one
set_key "$NEW,$OLD"; roll    # 2. the signer signs with the new one; both still accepted
set_key "$NEW";      roll    # 3. only the new key: the old one stops working
```

Notes:

- The pods read the Secret at start, so every phase needs the restart. `helm upgrade` never reverts a value you set: the chart reads the current Secret with `lookup`.
- Between phases wait for `rollout status`; phase 2 and phase 3 may follow immediately after the previous finishes. Do not skip phase 1, or a pod that signs with the new key is rejected by a receiver that still runs with the old one.
- Check after every phase with `sudo gitstack status` and a `git ls-remote` of a repository; a `401` or `403` in the logs of identity or core during a phase means a receiver does not know the key yet.
- With `serviceSecrets.existingSecret` the Secret is yours: use its name in place of `$R-service-secrets`; it must have the three keys `gateway`, `core`, `git`.
- Rotate the three keys one after the other, not at the same time.

**Emergency (key of one service leaked).** Replace that key at once in a single step (`set_key "$NEW"; roll`) accepting a short window of `401` while the pods restart: there is no point in keeping the old key valid. The other two keys stay as they are: a key never signs for another caller.

#### Upgrade from a version before F9

`sudo gitstack upgrade` migrates an installation that still has the shared Secret `<release>-identity-service`:

1. it removes `identity.serviceSecret` from the values of the release (the new chart ignores `identity.serviceSecret.existingSecret`; use `serviceSecrets.existingSecret` with the keys `gateway`, `core`, `git` for your own Secret);
2. `helm upgrade` creates `<release>-service-secrets` with three random keys and restarts every service with its own key; for the duration of the rolling update requests between old and new pods can answer `401`, which is why the upgrade waits for all services to be healthy;
3. when everything is healthy it deletes the shared Secret `<release>-identity-service` and says so in the summary. If the deletion is refused it prints the `kubectl delete secret` command to run by hand. The same cleanup runs when `gitstack upgrade` finds GitStack already at the requested version, so a second run is enough after an upgrade done by an older binary.

A rollback (`helm rollback`) puts the previous chart back, which generates a new shared Secret by itself; nothing to do by hand. Proof in the tests: `admin/internal/upgrade/upgrade_test.go#TestUpgrade_MigraISegretiDiServizio`, `#TestUpgrade_SecretCondivisoNonCancellatoSenzaQuelloNuovo`, `#TestUpgrade_CancellazioneDelSecretNonFatale`.

#### OIDC key

The key encrypts the client secrets saved in `identity.oidc_providers` and the state cookie of a login in progress. Identity writes the providers of its configuration again at every start, so a rotation is: new key in `<release>-identity-oidc-key` (`key`, 32 bytes in base64: `openssl rand -base64 32`), a different `identity.oidc.encKey.keyId` in the values (e.g. `k2`), `helm upgrade`. At the restart identity re-encrypts every client secret with the new key. Logins in progress at that moment fail once. The old key is not kept: a provider whose row still has the old `keyId` answers an error until identity has restarted with the new pair. Keep a backup of the Secret before changing it.

#### TOTP keyring (second factor, F11, GIT-237)

Local accounts can turn on a second step at sign-in from Settings → Security: a time-based code (TOTP, RFC 6238) from an authenticator app, plus ten single-use recovery codes. Identity encrypts each user's TOTP secret with AES-256-GCM under a key of the ring in `GITSTACK_IDENTITY_TOTP_KEYS` (`<id>:<base64 of 32 bytes>[,<id>:<base64>...]`, the first key encrypts, all keys decrypt) and stores only the key id with the secret. The chart generates the first key once (Secret `<release>-identity-totp-key`, `keys`, `helm.sh/resource-policy: keep`) or reads `identity.totp.keys.existingSecret`. Without keys nobody can turn TOTP on (Settings → Security says so).

Make it mandatory for administrators with `identity.totp.requiredForAdmins=true` (`GITSTACK_IDENTITY_TOTP_REQUIRED_FOR_ADMINS=true`): an administrator who signed in with a password and has no TOTP gets 403 `totp_enrollment_required` on every call except `GET /auth/session`, `POST /auth/logout` and `/auth/totp*` until the second factor is on, and cannot turn it off. Sessions that come from OIDC are never prompted (the second factor of OIDC users is their provider's), and personal tokens never have one (they are the credential).

Rotation: generate a new key (`openssl rand -base64 32`), put `new-id:<base64>,old-id:<base64>` in the Secret and restart identity: new secrets use the new key, existing ones still decrypt with the old one, which must stay in the list while any row names it (`identity.totp_credentials.enc_key_id`). Losing every key a row names makes that user's second factor unverifiable and the login fails with an internal error until an administrator clears it.

A user who lost the device and the recovery codes is unlocked by an administrator with `DELETE /v1/users/<username>/totp` (this also revokes the user's sessions); the user can then sign in with the password and set TOTP up again. If the only administrator is locked out and no other administrator exists, clear the rows by hand: `DELETE FROM identity.totp_recovery_codes WHERE user_id = ...; DELETE FROM identity.totp_credentials WHERE user_id = ...;` in the identity database.

#### Webhook keyring

Core encrypts webhook and mirror secrets with the primary key and decrypts each row with the key named by its `secret_key_id`, so old rows stay readable after a rotation. Rotation: put the current key in `oldKeys` as `<keyId>:<key>` (comma separated for more than one), put a new 32-byte key (`openssl rand -hex 32`) in `key`, set a new `core.webhookSecret.keyId`, `helm upgrade`. New and edited secrets use the new key; old rows are decrypted with the entry of `oldKeys` (`services/core/internal/webhooks/unit_test.go#TestKeyring_CifraDecifraERotazione`). Do not remove an old key from `oldKeys` while rows still use it: the secret would be unreadable and the webhook has to be configured again. A lost key has the same effect.

**Pushing an imported history (F18).** The git service runs `receive-pack` with `receive.fsckObjects=true`: a push that carries a malformed object is rejected with the fsck message. Two harmless oddities common in imported repositories, zero-padded file modes (`zeroPaddedFilemode`) and an invalid timezone (`badTimezone`), are accepted as warnings. If a push of an old history is still rejected for another fsck message (for example `missingEmail`), an administrator can relax the check for the import by setting `GITSTACK_GIT_RECEIVE_FSCK=off` on the `git` service (chart: an extra env of the git deployment), pushing the history, and then removing the variable so the check is back on. Pull mirrors are not affected: a fetch is not checked with `fsckObjects`.

### Notices on credential changes (F13, GIT-239)

When a password is changed or reset, a personal token or an SSH key is added,
or a token is about to expire (7 days before), the account owner gets an
in-app notification (reason `credential`, "Security" in the inbox) and, with
the SMTP configured, an e-mail. These notices are security alerts: the e-mail
preferences do not switch them off. They never contain a secret: the token
value is not in them, only its name and last four characters.

Identity records the event and core delivers it (one pass every 30 seconds);
nothing to configure. If an e-mail is not expected, check that the SMTP of core
is on (`smtp.*` in the chart values) and that the user has an address.
Delivery is at least once: after an identity outage a notice can arrive twice.

### Personal data at rest and data export (F20, GIT-239)

GitStack stores user names, e-mail addresses and display names in clear in
Postgres (`identity.users`, and the names inside `core`). There is no
application-level encryption of these fields: for an on-prem tool the
protection of that data at rest is the encryption of the disks that hold it.
This is a requirement of the installation, not an option of GitStack
(ASVS V6.1.1).

**What to encrypt**

| What | Where | How |
|---|---|---|
| Data disk | the volume mounted at `/var/lib/rancher`: k3s, the bundled Postgres volume, the Git repositories and the attachments | full-disk or volume encryption done before the install: LUKS (`cryptsetup luksFormat`, then `luksOpen`, `mkfs`, mount at `/var/lib/rancher` and an entry in `/etc/crypttab`), or the encryption of the cloud/hypervisor disk |
| Local backup copy | `backup.destination` (default `/var/backups/gitstack`) | put it on an encrypted volume as well, and create the archives with `gitstack backup --key-file` (AES-256-GCM), which protects them when the file is copied elsewhere |
| External PostgreSQL | managed by whoever runs the database server | encrypted storage and encrypted backups on that server; `sslmode=verify-full` for the connection (see [External PostgreSQL](#external-postgresql)) |
| Off-host copies | S3 and every copy you take | always encrypted (S3 uploads are encrypted by GitStack, [Backup to S3](#backup-to-s3-n7)); keep the key file (`0600`) away from the archives |

A minimal LUKS setup for a dedicated data disk (adapt the device name; this
destroys what is on it):

```sh
sudo cryptsetup luksFormat /dev/sdb
sudo cryptsetup luksOpen /dev/sdb gitstack-data
sudo mkfs.ext4 /dev/mapper/gitstack-data
echo 'gitstack-data /dev/sdb none luks' | sudo tee -a /etc/crypttab
echo '/dev/mapper/gitstack-data /var/lib/rancher ext4 defaults 0 2' | sudo tee -a /etc/fstab
sudo mkdir -p /var/lib/rancher && sudo mount /var/lib/rancher
```

Check it after the install: `lsblk -f` must show `crypto_LUKS` under the disk
that backs `/var/lib/rancher`, and the same for the backup destination.
Losing the LUKS passphrase means losing the data: keep it in your password
manager together with the backup key.

**Export of your own data (V8.3.2).** Every user can download the personal data
that the identity service keeps about them: `GET /v1/user/export` (scope
`read:user`; in the web UI session or with a personal token).

```sh
curl -fsS -H "Authorization: Bearer $GITSTACK_TOKEN" \
  https://git.example.com/api/v1/user/export -o gitstack-me.json
```

The JSON has the profile (username, e-mail, display name, role), the personal
tokens (name, scopes, last four characters, dates; never the value), the SSH
keys (with the public key), the active web sessions and whether TOTP is on.
It never contains passwords, hashes, TOTP secrets or recovery codes. Issues,
comments and repositories live in `core` and are read with their own APIs.
Each export is logged as `security.data.exported`. To remove an account an
administrator deletes the user (`DELETE /v1/users/{username}`); there is no
self-service removal.

## Built-in CI: install, upgrade, backup (v2-a)

The built-in CI (workflows in `.gitstack/workflows/`, jobs as throw-away pods
in the namespace `gitstack-ci-jobs`, rules CI1-CI7) is **off by default** and
is switched on by the installer or by `gitstack upgrade`. The chart values are
documented in `deploy/gitstack/values.yaml` (`ci.*`).

### Switching it on at install time

```bash
sudo ./deploy/install.sh --ci                  # job slots chosen from the machine profile
sudo ./deploy/install.sh --ci --ci-capacity 2  # explicit slots (1-3)
```

| Option | Meaning |
|---|---|
| `--ci` / `--no-ci` | switch the CI on / off. With neither, a re-run **keeps** the previous choice (the job namespace exists) and its slots; a first install has no CI |
| `--ci-capacity N` | jobs in parallel, 1 to 3 (`ci.capacity`) |
| `--ci-build` | also enables rootless BuildKit builds in the separate namespace `gitstack-ci-build` (`ci.build.enabled`) |
| `--ci-action-mirror URL` | internal mirror of the `uses:` actions instead of github.com (N4, N9) |
| `--ci-storage-size SIZE` | size of the volume with job logs (default `20Gi`) |
| `--ci-s3-endpoint`, `--ci-s3-bucket`, `--ci-s3-region`, `--ci-s3-prefix` | S3 for artifacts and caches (N7); keys in `GITSTACK_CI_S3_ACCESS_KEY` and `GITSTACK_CI_S3_SECRET_KEY` |

**Job slots per profile (N1, CI7).** Without `--ci-capacity` the installer
picks **3** slots on the recommended profile (at least 8 vCPU **and** 16 GB of
RAM) and **1** slot on the minimum profile (4 vCPU, 8 GB), also when only one of
the two numbers reaches the recommended value. The slot count is never above 3.

**Job space and priority.** Each slot is one pod with 2 vCPU, 4 GiB of memory
and 20 GiB of ephemeral disk: the `ci-jobs` ResourceQuota in the job namespace
is `capacity` times that, and a LimitRange caps every container at the same
values. Job pods use the PriorityClass `gitstack-ci-job` (value 1000, no
preemption), below `gitstack-apps` (100000) and `gitstack-services`
(1000000): under memory pressure the jobs are evicted first, never the
services. The preflight adds a **warning** (not an error) when the free disk
is below 20 GB for GitStack plus 20 GB per slot.

**Corporate network (N4, N9).** The proxy (`--http-proxy`, `--https-proxy`,
`--no-proxy`) and the corporate CA (`--ca-bundle`) given to the installer reach
the job pods too: `core` passes `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` to
every job pod, and the chart copies the CA ConfigMap into the job namespace
(`corporate-ca`), mounted at `/etc/gitstack/corporate-ca` and exported as
`SSL_CERT_DIR` and `NODE_EXTRA_CA_CERTS`. Actions are downloaded from the
mirror of `--ci-action-mirror` (when the host is an IPv4 address the installer
also opens the egress of the job namespace towards `<ip>/32` on the mirror
port; with a host name use `--values` with `ci.runner.actionMirrorEgress`,
because the base policy closes 10/8, 172.16/12, 192.168/16 and 169.254/16).
The same holds for the proxy: when `--http-proxy` / `--https-proxy` point to an
IPv4 address, the installer opens the egress of the job namespace towards
`<ip>/32` on the proxy port (`ci.runner.proxyEgress`). When the proxy is given
by **name** and sits on a private network, the installer cannot know its
address (it does not resolve the name) and logs a notice: add the rule with
`--values`, e.g. `ci.runner.proxyEgress: [{to: [{ipBlock: {cidr: 10.0.0.5/32}}],
ports: [{protocol: TCP, port: 3128}]}]`. Without it the job pods cannot reach
the proxy and every download from a job fails.
The executor (`act` plus `ci-runner`) is **inside the runner image**
`gitstack-ci-runner`, published and cosign-signed by `ci.yml` with the same tag
as the services, so nothing is downloaded at job time except the `uses:`
actions: through `--registry-mirror` (or `--image-registry`) the runner image
comes from the internal registry like all the others.

**S3.** With a bucket, artifacts and caches go to S3 (the volume keeps only a
scratch area) and **logs stay on disk**, inside the backup. The installer
creates the Secret `<release>-ci-s3` (keys `accessKey`, `secretKey`) from the
two environment variables; the keys never appear on a command line.

### Upgrading from v1.x with the CI on

A v1.x installation has no CI. The new chart brings it with two values:

```bash
sudo gitstack upgrade --to v2.0.0 --set ci.enabled=true --set ci.capacity=1
```

`gitstack upgrade` works as usual (health check, preventive backup, k3s patch,
Helm, health check, automatic rollback), and also: it lists the runner image
among the images to check (it is the value `GITSTACK_CORE_CI_IMAGE` of core,
not an `image:` line) and verifies its cosign signature; `--set` values are
kept for the next upgrades (`helm get values`), so later upgrades keep the CI
without repeating the flags. `gitstack upgrade --dry-run` shows the plan with
the same flags. Re-running `install.sh` after an upgrade keeps the CI as well
(the job namespace exists).

If the upgrade fails the rollback (exit code `7`) restores the previous Helm
revision and the database schemas: a CI that was switched on by the failed
upgrade is **off again**, because the previous revision had no `ci.*` values.
Switch it on again after fixing the cause.

### Backup and restore with the CI logs

`gitstack backup` includes the job logs (`ci-logs.tar`, from the `logs/` folder
of the volume `<release>-ci-data`); artifacts and caches (`data/`, or S3) are
**not** in the backup and the rows that list them are dropped from the dump.
`gitstack restore` puts the logs back together with the database: the runs and
the logs written before the backup are readable afterwards; those written after
it are gone, like all the other data. An archive with CI logs restored on an
installation without the CI skips the logs and says so.

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
   riavvio NATS per le credenziali ripristinate
   riavvio i servizi
   Restore completato: database, repo, allegati, Secret e configurazione ripristinati.
   ```

   After the Secrets are restored, the restore restarts the NATS StatefulSet
   (`<release>-nats`, if the chart runs it) and waits for it to be ready,
   **before** it starts identity, core, git and the gateway again: NATS reads
   its credentials from the restored `<release>-nats-auth` Secret only at
   startup. No manual `rollout restart` is needed (older versions exited with
   code `70`, "gateway non è tornato pronto", and needed
   `kubectl rollout restart statefulset/<release>-nats`, then core and
   gateway: superseded by this fix, GIT-297). PostgreSQL is not restarted (its
   Secret is not restored) and the web reads no Secret.

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

### The k3s install script is pinned by checksum (F19, GIT-239)

`install.sh` does not pipe `https://get.k3s.io` into `sh`: it downloads the
script, compares its SHA-256 with the one pinned in `deploy/k3s-install.sha256`
and runs it only if they match. A different script (changed upstream, or
tampered with on the way or on a mirror) stops the install with
`lo script di k3s ... non corrisponde allo sha256 fissato` before anything is
executed.

- When the pin is stale because k3s changed its script, update it with the
  version bump: `curl -sfL https://get.k3s.io | sha256sum`, review the script,
  and put the hash in `deploy/k3s-install.sha256`.
- With `--download-mirror` the mirror's `k3s/install.sh` must be the same
  script; if it is a deliberately different one, set
  `GITSTACK_K3S_SCRIPT_SHA256=<sha256 of your script>` for the install.
- `gitstack upgrade --k3s` is not covered by this pin (it uses its own
  download path, `admin/internal/upgrade/k3s.go`).
- The test is `deploy/tests/k3s_script_test.sh`, run in CI.

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

- **Egress policy.** Like webhooks, mirror destinations go through the egress protection (`egress.*` in the chart values). Loopback, link-local, the cluster networks, the node's own addresses and the Kubernetes ports (6443, 10250, 10255, 2379-2380) on private addresses are always blocked. A server on your LAN is allowed by default; if you set `egress.allow` (see [Egress policy and the LAN](#egress-policy-and-the-lan)), only what it lists is allowed, so add the destination's address (an IP/CIDR, or a host name). A blocked destination ends in a `blocked` run and is not retried.
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
  **one patch older** than `deploy/k3s-version` → data → a **failing**
  `gitstack upgrade` (`--set ci.enabled=true --set gateway.containerPort=1`: the
  gateway cannot bind, exit code 7, previous version and data back, CI off
  again) → `gitstack upgrade --set ci.enabled=true` (preventive backup → k3s
  patch → GitStack, N10) → k3s checked with `k3s --version`, `kubectl version`
  and `gitstack status --json` (`k3s_version`) → backup → more data → restore.

Both scenarios run with the **built-in CI on** (`install.sh --ci` in fresh, the
upgrade above in upgrade; see «Built-in CI: install, upgrade, backup»): after
the install (or the upgrade) a repository gets a workflow in
`.gitstack/workflows/`, a push, and the test waits for the run to finish with
`success` in a throw-away job pod and for the marker in the job log. After the
restore the run and its log (backed up as `ci-logs.tar`) are still readable and
a new workflow runs again. The runner image `gitstack-ci-runner` is published
and signed like the five service images (also by the job `images` of the
manual run).

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

Not officially supported in v1.0 (decision of 2026-10-18): the installer accepts
Pop!_OS without `--force` but prints a warning in the preflight, with no
guarantee and no CI test.

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

### Load smoke test (V3)

Rule V3 asks for a smoke test, not a benchmark. `TestCaricoFumo`
(`services/core/internal/stackitest/smoke_load_integration_test.go`) starts the
full test stack (gateway, identity and git service as real binaries, core
in-process, Postgres) and runs five people and three `gs` agents at the same
time for a configurable time: clone, push to their own branch and to `main`
(HTTPS and SSH for people, HTTPS for agents), fetch, API reads, issue creation
and comments.

It passes when there are **zero 5xx responses and zero 429s**, no operation fails, **no push
is lost** (every successful push is on `main`, and `main` has exactly the seed
plus those commits), and there is **no race on refs** (each worker branch sits
at the sha of its last push, `git fsck` is clean, issue numbers are unique and
the totals match). A latency summary (n, p50, p95, max per operation) is printed
in the log; it is informational only and has no target.

The smoke stack runs the gateway with its **default rate limits**
(per credential 20 requests/s with a burst of 200, per IP as shipped); the test
does not set any `GITSTACK_GATEWAY_RATE_LIMIT_*` variable, so it also proves
that the real load of the eight workers is not rejected. A `429` received by a
worker during the load phase is counted in the summary (`429: N`) and fails
the test, like a 5xx. The verification reads after the load (one token, hundreds
of requests per second in sequence) behave like a polite client instead: on
`429` they wait for `Retry-After` and retry, up to 30 attempts and 90
seconds per read, and only then fail.

**The capacity figures for N1 (hardware profiles) are indicative, not
measured.** This test does not establish how many users a machine supports.

Run it locally (needs Docker, `git`, `ssh` and `ssh-keygen`; on Windows set
`DOCKER_HOST=npipe:////./pipe/dockerDesktopLinuxEngine` and
`TESTCONTAINERS_RYUK_DISABLED=true`):

```bash
cd services/core
GITSTACK_SMOKE_DURATION=30s go test -tags=integration -run TestCaricoFumo -v -count=1 ./internal/stackitest/
```

Without `GITSTACK_SMOKE_DURATION` the test is skipped, so the `go` job of
`ci.yml` does not run it. In CI it is the separate workflow
`.github/workflows/smoke.yml`: on every push to `main`, every night
(04:11 UTC) and on demand, with `-race` and a default of 3 minutes.

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
| `gitstack config set apps-domain DOMAIN` | yes | Set or change the apps domain and reissue the wildcard certificate (see [Apps domain](#apps-domain-and-wildcard-dns-v3-h2)) |
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

**Prove fatte** (repo di prova `fathorMB/gitstack-release-test`, pubblico, con
Actions e permessi di scrittura al `GITHUB_TOKEN`; i push li fa il board):

| Tag | Data | Esito |
|---|---|---|
| `v0.0.0-test.1` | 2026-10-13 | Run [38400176636](https://github.com/fathorMB/gitstack-release-test/actions/runs/38400176636): il job `security` (bloccante sui tag) è rosso, quindi `registry`, `gs-binaries`, `admin-binary`, `sign` e `release` sono *skipped*. Il blocco è corretto: erano i finding già elencati in `docs/security-scans.md` (Go 1.26.2, pgx 5.7.6, immagine nginx 1.27, vitest 3 con tinypool, regex e gosec da motivare). Nessun difetto nella logica del job `release`, che però **non è stato ancora eseguito**. |
| `v0.0.0-test.2` | 2026-10-13 | Run [38419075310](https://github.com/fathorMB/gitstack-release-test/actions/runs/38419075310): tutti i gate di `security` verdi (govulncheck, Trivy x5, pnpm audit, eslint, gosec), poi `registry` rosso con `denied: permission_denied: write_package`. Causa: le immagini vanno in `ghcr.io/<owner>/gitstack-<servizio>` e un repo di prova dello **stesso owner** (`fathorMB`) punta ai package veri di GitStack, che il `GITHUB_TOKEN` di un altro repo non può scrivere (ed è giusto così: la prova non deve toccarli). Correzione: variabile di repository `IMAGE_NAMESPACE` (vedi sotto). |
| `v0.0.0-test.3` | 2026-10-13 | Run [38423177002](https://github.com/fathorMB/gitstack-release-test/actions/runs/38423177002): `security` verde, ma `registry` rosso con `permission_denied: The requested installation does not exist`: la variabile `IMAGE_NAMESPACE=release-test` **sostituiva** l'owner (immagine `ghcr.io/release-test/gitstack-core`, un owner inesistente). In più `admin-binary` rosso con `ReleaseAsset.name already exists`: il push di `main` e quello del tag sullo stesso commit avviano due run che creano e caricano nella stessa release `sha-<commit>` in parallelo. Correzioni: `IMAGE_NAMESPACE` si imposta al percorso completo (`fathormb/release-test`), e i job `registry`, `admin-binary`, `gs-binaries` e `sign` hanno un gruppo `concurrency` per commit (si mettono in fila, senza cancellare). |
| `v0.0.0-test.4` | 2026-10-13 | Run [38430369701](https://github.com/fathorMB/gitstack-release-test/actions/runs/38430369701): gate, `registry`, `admin-binary`, `gs-binaries` e `sign` verdi; `release` rosso al primo passo: `gitstack-gateway:v0.0.0-test.4` non ha lo stesso digest di `sha-<commit>`. Causa: i run di `main` e del tag, sullo stesso commit, ricostruivano entrambi l'immagine; la seconda build (label con l'ora) ha digest diverso e sovrascrive `sha-<commit>`, mentre i tag di versione restano sulla prima. Correzione: il job `registry` non ricostruisce se `sha-<commit>` esiste già e aggiunge solo i tag mancanti sullo stesso digest (`docker buildx imagetools create`). Il job «Installazione reale» (Ubuntu 22.04) installa dai package veri `fathormb/gitstack-*` e salta se `IMAGE_NAMESPACE` è impostata. |
| `v0.0.0-test.5` | 2026-10-18 | Run [39109148757](https://github.com/fathorMB/gitstack-release-test/actions/runs/39109148757): il gate Trivy dell'immagine web è rosso per un HIGH nuovo uscito dopo test.4 (`libexpat` 2.8.5-r0, corretto in 2.9.0-r0), quindi tutto il resto è *skipped*. Non è un difetto del workflow ma un invecchiamento dell'immagine di base: i database delle vulnerabilità cambiano ogni giorno e il gate lo mostra. Correzione: `web/Dockerfile` esegue `apk upgrade --no-cache` come root e torna all'utente 101 (verificato: libexpat 2.9.0-r0, uid 101). Da ripetere subito prima di ogni tag vero. |
| `v0.0.0-test.6` | 2026-10-18 | Run [39136356659](https://github.com/fathorMB/gitstack-release-test/actions/runs/39136356659): tutti i gate, `registry`, `admin-binary`, `gs-binaries` e `sign` verdi (le correzioni dei giri precedenti funzionano, compreso il riuso di `sha-<commit>`), `release` rosso al primo passo: il tag di versione non ha il digest di `sha-<commit>`. Causa: `docker buildx imagetools create` (usato per aggiungere i tag) **rigenera l'indice** e ne cambia il digest (visto su GHCR; in locale su un registry di prova no). Correzione: i tag si aggiungono con `skopeo copy --all --preserve-digests` (immagine fissata per digest), con controllo del digest subito dopo. |
| `v0.0.0-test.7` | 2026-10-18 | Run [39144485283](https://github.com/fathorMB/gitstack-release-test/actions/runs/39144485283) su `main` allineato (codice nuovo di GIT-237/GIT-239): fermo prima di `registry` da due rossi che non riguardano il workflow di rilascio. (1) `go`: tre test email di core (`TestEmail_*`) contavano 8 tipi di preferenza invece di 9 con `credential`: bug di `main`, già corretto da GIT-256 (merge). (2) gosec: `G115` in `totp.go` (contatore HOTP da `Step()`, mai negativo) e `G101` sul nome `token_expiring` (costante di tipo evento, anche nei file generati): falsi positivi, aggiunti come eccezioni motivate. |
| `v0.0.0-test.8` | 2026-10-18 | Run [39147209296](https://github.com/fathorMB/gitstack-release-test/actions/runs/39147209296): per la prima volta tutta la catena è verde (gate, `go`, `registry` con riuso dell'immagine, `admin-binary`, `gs-binaries`, `sign`) e `release` esegue 14 passi su 15: digest dei tag di versione uguali a `sha-<commit>` con firma verificata, binari, chart `0.0.0-test.8`, SBOM, `SHA256SUMS`, firma keyless di 11 artefatti, changelog e verifica finale (`OK: 11 firme e SHA256SUMS verificati`). Rosso solo «Pubblica la GitHub Release»: `ReleaseAsset.name already exists` su `gs_linux_arm64.sigstore.json`. Causa: il glob `dist/gs_*` include già i bundle dei binari `gs`, che tornano anche in `dist/*.sigstore.json`: l'elenco dei file aveva nomi doppi. Correzione: l'elenco si deduplica (`sort -u`) prima di `gh release create`. |
| `v0.0.0-test.9` | 2026-10-18 | **Verde.** Run [39149236388](https://github.com/fathorMB/gitstack-release-test/actions/runs/39149236388): tutti i job fino a `release` riusciti (il job «Installazione reale» salta di proposito con `IMAGE_NAMESPACE`). Release [v0.0.0-test.9](https://github.com/fathorMB/gitstack-release-test/releases/tag/v0.0.0-test.9): pre-release, non *latest* (il repo non ha nessuna latest), 32 asset (binari, 6 `gs_*`, chart `0.0.0-test.9`, `.sigstore.json` di ognuno, 8 SBOM, `SHA256SUMS` firmato, `CHANGELOG-v0.0.0-test.9.md`, `CHANGELOG.md`); PR `changelog/v0.0.0-test.9` aperta contro `main` con il solo `CHANGELOG.md`. `cosign verify-blob` (v2.4.0, identità `ci.yml@refs/tags/v.*` del repo di prova) riuscito per `gitstack-linux-amd64`, `gs_linux_amd64`, il chart e `SHA256SUMS`, e `sha256sum -c` torna OK; `cosign verify` riuscito per tutte e 5 le immagini, con `0.0.0-test.9`, `v0.0.0-test.9` e `sha-<commit>` sullo stesso digest. |

Difetti trovati e corretti in 9 giri (GIT-247): finding di sicurezza aperti (test.1), nome dei package uguale a quello di produzione (test.2–3), run di `main` e del tag in parallelo sulla stessa release (test.3), digest dei tag di versione (test.4 e test.6: `imagetools create` rigenera l'indice, ora `skopeo --preserve-digests`), invecchiamento dell'immagine di base (test.5), rossi del codice nuovo di `main` (test.7) e nomi doppi nell'elenco degli asset (test.8).

**Repo di prova dello stesso owner.** Le immagini si chiamano
`<registry>/<owner>/gitstack-<servizio>`: su `fathorMB/gitstack-release-test`
coinciderebbero con i package veri. Sul repo di prova si imposta la variabile di
repository `IMAGE_NAMESPACE`, il **percorso completo** al posto di `<owner>` (per
esempio `fathormb/release-test`, minuscolo): i job `registry`,
`sign` e `release` usano `${{ vars.IMAGE_NAMESPACE || github.repository_owner }}`
e le immagini finiscono in `ghcr.io/fathormb/release-test/gitstack-<servizio>`,
cancellabili senza toccare i package di produzione. Sul repo vero la variabile
non esiste e non cambia nulla. Il job `install` (`sha-<commit>`) non la usa e
sul repo di prova non è significativo.

**Prima di ogni prova**: il tag parte da un commit che ha il gate di sicurezza
verde (`workflow_dispatch` di `security.yml` con `release: true` sul repo di
prova), altrimenti il run si ferma ai gate e non arriva a `release`. Un
`main` fermo a un commit vecchio non basta: i database di govulncheck, Trivy e
pnpm audit cambiano ogni giorno.

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

## Prove della tappa 2 di V7 (condizioni 2 e 4)

Le due prove che seguono le lancia il **board**, non un agente. Servono a sbloccare il passaggio della casa su GitStack (V7, tappa 2; `rilascio-v1.md`). Dal 18/10/2026 la tappa 2 si fa appena sono vere le condizioni 2 (ripristino di prova), 3 (prova dell'agente, GIT-254), 4 (mirror in push su un repo di prova) e 5 (istruzioni, già fatta): la condizione 1 (mirror in pull per 2 settimane) non c'è più. Ogni passo ha il comando e l'esito atteso: se l'esito è diverso, ci si ferma e si annota il comando, l'output e l'ora. Gli esiti attesi vengono dalle sezioni Backup, Restore, Mirror e Diagnostics di questo documento e dal codice di `admin/internal/cli/backup.go`; nessuna di queste prove è ancora stata eseguita su homehub.

### Condizione 2: ripristino di prova su una macchina nuova

**Cosa si prova:** un backup giornaliero di homehub, portato su una VM appena installata, si ripristina e l'istanza risponde con i dati di homehub.

**Prerequisiti**

- Una VM Ubuntu Server 22.04 o 24.04 amd64, pulita, con i requisiti di [Prerequisites](#prerequisites) e raggiungibile via SSH. Non è homehub e non ne prende il posto: il nome host della VM può essere diverso.
- Un backup di homehub di **meno di 24 ore** (il timer giornaliero) e, se è cifrato, il file della chiave. Il restore rifiuta un archivio di un'altra versione del server: la VM va installata **alla stessa versione di homehub**.
- Su homehub, per conoscere la versione e l'ultimo backup:

```sh
gitstack version
# gitstack sha-<40 caratteri>   → annota questa versione: è <VERSIONE>
sudo gitstack status
# nella riga «Ultimo backup:» ci sono l'ora e il percorso dell'archivio; l'ora deve essere di meno di 24 ore fa
# in fondo: «Stato: sano»
```

**Passo 1. Portare l'archivio e la chiave sulla VM**

Con il backup locale (cartella `backup.destination`, di default `/var/backups/gitstack`; su homehub è `/srv/backup/gitstack`), da homehub:

```sh
ARCH=$(ls -t /var/backups/gitstack/gitstack-backup-*.tar.gz* | grep -v '\.sha256$' | head -n 1)
echo "$ARCH"
scp "$ARCH" <utente>@<ip-vm>:/tmp/
scp /etc/gitstack/backup.key <utente>@<ip-vm>:/tmp/      # solo se l'archivio è cifrato (.enc); usa il percorso della tua chiave
```

Esito atteso: `scp` termina con exit `0`; sulla VM `ls -l /tmp/gitstack-backup-*` mostra l'archivio. Se i backup vanno su S3 (N7), salta questo passo e usa `--from s3` al passo 3.

**Passo 2. Installare GitStack sulla VM, alla stessa versione**

Sulla VM, dal checkout del repo alla stessa versione di homehub (o con il binario e il chart di quella release):

```sh
sudo ./deploy/install.sh --image-tag <VERSIONE> --host <ip-vm>
# se homehub gira una release (vX.Y.Z o vX.Y.Z-rc.N) invece di una build sha-…:
# sudo ./deploy/install.sh --version <vX.Y.Z> --host <ip-vm>
gitstack version
sudo gitstack status; echo "exit=$?"
```

Esito atteso: `gitstack version` stampa `gitstack <VERSIONE>` (la stessa di homehub); `gitstack status` finisce con `Stato: sano`, tutti i servizi `N/N pronti  OK`, `exit=0`. Con Postgres esterno vale [External PostgreSQL](#external-postgresql) e un database **vuoto**.

**Passo 3. Ripristinare**

```sh
sudo gitstack restore --key-file /tmp/backup.key /tmp/gitstack-backup-<data>-<VERSIONE>.tar.gz.enc
# archivio non cifrato:
sudo gitstack restore /tmp/gitstack-backup-<data>-<VERSIONE>.tar.gz
# archivio su S3 (dopo aver passato le opzioni --s3-* a install.sh):
sudo gitstack restore --from s3
echo "exit=$?"
```

Esito atteso: le righe `archivio verificato: versione <VERSIONE> del <data>, N file`, `fermo i servizi: …`, `ripristino del database (in una sola transazione)`, `ripristino dei repo (git-data)`, `ripristino degli allegati`, `ripristino dei Secret`, `ripristinati N file di configurazione (config.yaml resta quello della nuova installazione)`, `riavvio NATS per le credenziali ripristinate` (solo se NATS è quello del chart), `riavvio i servizi`, poi `Restore completato: database, repo, allegati, Secret e configurazione ripristinati.` e `exit=0`. Un codice diverso: vedi [Exit codes](#exit-codes) (`6` = versione diversa, checksum sbagliato o chiave mancante: la prova **non** è riuscita, riparti dal passo 2 con la versione giusta).

**Passo 4. Controlli di riuscita**

Tutti sulla VM, dopo il restore:

| # | Controllo | Comando | Esito atteso |
|---|---|---|---|
| 1 | Servizi sani | `sudo gitstack status; echo "exit=$?"` | `Stato: sano`, `exit=0` |
| 2 | API raggiungibile | `curl -sk -o /dev/null -w '%{http_code}\n' https://<ip-vm>/api/healthz` | `200` |
| 3 | I repo di homehub ci sono | `curl -sk -H "Authorization: Bearer $TOKEN" https://<ip-vm>/api/v1/repos` | l'elenco contiene gli stessi repo di homehub (stessi owner e nomi) |
| 4 | Lo stesso storico | `git ls-remote https://<ip-vm>/<owner>/<repo>.git` e lo stesso comando su homehub (CA interna in `GIT_SSL_CAINFO`) | stessi SHA per `refs/heads/main` e per i tag, a meno dei push arrivati dopo il backup |
| 5 | Le issues ci sono | `GS_HOST=<ip-vm> GS_TOKEN=$TOKEN gs issue list -R <owner>/<repo>` | stesse issues di homehub al momento del backup |
| 6 | Login | accedere da browser a `https://<ip-vm>/` con un utente di homehub | login riuscito (le credenziali sono quelle di homehub) |
| 7 | Scrittura | `git clone` di un repo della VM, un commit, `git push` | exit `0`, push accettato; scrive solo sulla VM |

`$TOKEN` è un token di un utente di homehub (esiste nel database ripristinato) o uno creato dalla UI della VM dopo il login. I confronti dei controlli 3–5 si fanno con lo stato di homehub nell'istante del backup: push o issues arrivati dopo non ci saranno sulla VM.

**Esito:** riuscita se i controlli 1–7 sono tutti ok. Il board annota data, versione, nome dell'archivio e risultato di ogni controllo.

**Pulizia.** La VM si spegne e si cancella; nessun comando della prova scrive su homehub (il backup viene solo letto e copiato). Cancella anche la copia della chiave: `shred -u /tmp/backup.key`.

### Condizione 4: mirror in push verso un repo GitHub di prova, senza `diverged`

**Cosa si prova:** il mirror in push (V8-mirror, GIT-179) manda il ramo principale e i tag di un repo di homehub su un repo GitHub vuoto senza forzare; una destinazione con storia diversa lo ferma in `diverged` e non viene sovrascritta. Vedi anche [Mirror](#mirror).

**Prerequisiti**

- Un repo GitHub **vuoto** di prova (senza README, licenza o `.gitignore`), per esempio `<utente>/gitstack-mirror-prova`, e un **fine-grained token** con *Only select repositories* su quel repo e *Contents: Read and write* (nient'altro), con scadenza. Il ramo principale del repo di prova su GitHub **non** deve essere protetto.
- Su homehub un repo **di prova**, non quello del codice di GitStack (che ha il mirror in pull): per esempio `<owner>/mirror-prova`, con almeno un commit su `main` e un tag. Se non c'è, con un token con permesso di scrittura:

```sh
export GS_HOST=<homehub> GS_TOKEN=<token>
gs repo create <owner>/mirror-prova
gs auth setup-git
gs repo clone <owner>/mirror-prova && cd mirror-prova
echo prova > README.md && git add README.md && git commit -m "prova del mirror" && git push origin HEAD:main
git tag v0.0.1 && git push origin v0.0.1
```

Esito atteso: i push terminano con exit `0`. (Se il ramo predefinito non si chiama `main`, usa il suo nome.)

- La chiave dei segreti di homehub è configurata (`GITSTACK_WEBHOOK_SECRET_KEY`): senza, la creazione del mirror risponde `503 mirror_secrets_unavailable`.
- Il token GitHub in una variabile, mai in un file: `read -rs GITHUB_TOKEN; export GITHUB_TOKEN`.

**Passo 1. Creare il mirror**

Dalla UI: repo → **Settings → Mirrors → Add mirror**, con *Remote URL* `https://github.com/<utente>/gitstack-mirror-prova.git`, *Username* il tuo utente GitHub, *Token* il token. Oppure con l'API:

```sh
curl -fsS -X POST "https://<homehub>/api/v1/repos/<owner>/mirror-prova/mirrors" \
  -H "Authorization: Bearer $GS_TOKEN" -H "Content-Type: application/json" \
  -d '{"url":"https://github.com/<utente>/gitstack-mirror-prova.git","username":"<utente>","token":"'"$GITHUB_TOKEN"'"}'
```

Esito atteso: risposta `201` con un oggetto mirror (`id`, `state`, `hasToken: true`; il token non compare). Annota l'`id` come `<MIRROR_ID>`. Il primo push parte da solo.

**Passo 2. Il primo push**

```sh
curl -fsS -H "Authorization: Bearer $GS_TOKEN" \
  "https://<homehub>/api/v1/repos/<owner>/mirror-prova/mirrors/<MIRROR_ID>"
```

Esito atteso: entro poche decine di secondi `"state":"in_sync"`, `lastError` nullo e `lastPushed` con `refs/heads/main` e `refs/tags/v0.0.1`; nella UI lo stato è **In sync**. Se è `pending` o `syncing` ripeti dopo qualche secondo; se è `error`, leggi `lastError` e le esecuzioni (`GET .../mirrors/<MIRROR_ID>/runs`): la prova non è riuscita.

**Passo 3. Gli stessi SHA su GitHub**

```sh
git ls-remote https://github.com/<utente>/gitstack-mirror-prova.git refs/heads/main 'refs/tags/*'
git ls-remote https://<homehub>/<owner>/mirror-prova.git refs/heads/main 'refs/tags/*'
```

Esito atteso: gli SHA di `refs/heads/main` e del tag `v0.0.1` sono identici nelle due uscite.

**Passo 4. Un push successivo arriva senza forzare**

```sh
cd mirror-prova && echo due >> README.md && git commit -am "secondo commit" && git push origin HEAD:main
sleep 20
curl -fsS -H "Authorization: Bearer $GS_TOKEN" "https://<homehub>/api/v1/repos/<owner>/mirror-prova/mirrors/<MIRROR_ID>"
git ls-remote https://github.com/<utente>/gitstack-mirror-prova.git refs/heads/main
```

Esito atteso: `state` è `in_sync` e lo SHA su GitHub è quello del nuovo commit (fast-forward, nessun `diverged`).

**Passo 5. La divergenza si ferma e non sovrascrive**

Questo passo prova la garanzia «mai forzato»: è l'unico in cui `diverged` è l'esito **voluto**.

```sh
git clone https://github.com/<utente>/gitstack-mirror-prova.git /tmp/gh-prova && cd /tmp/gh-prova
echo divergenza > divergenza.txt && git add divergenza.txt && git commit -m "commit diretto su GitHub" && git push origin HEAD:main
SHA_GH=$(git rev-parse HEAD); echo "$SHA_GH"
cd <clone-di-homehub> && echo tre >> README.md && git commit -am "terzo commit" && git push origin HEAD:main
sleep 20
curl -fsS -H "Authorization: Bearer $GS_TOKEN" "https://<homehub>/api/v1/repos/<owner>/mirror-prova/mirrors/<MIRROR_ID>"
git ls-remote https://github.com/<utente>/gitstack-mirror-prova.git refs/heads/main
```

Esito atteso: `state` è `diverged` (UI: «Stopped: diverged»), `lastError` indica la storia diversa della destinazione e `git ls-remote` mostra ancora `$SHA_GH`: il commit diretto su GitHub non è stato sovrascritto. Gli admin del repo ricevono una notifica con motivo `mirror` (`gs notification list`).

**Passo 6. Il ritorno a `in_sync`** (facoltativo; prova la risoluzione descritta in [If a mirror stops: divergence](#if-a-mirror-stops-divergence))

```sh
cd <clone-di-homehub> && git remote add github https://github.com/<utente>/gitstack-mirror-prova.git
git fetch github && git merge github/main && git push origin HEAD:main
curl -fsS -X POST -H "Authorization: Bearer $GS_TOKEN" \
  "https://<homehub>/api/v1/repos/<owner>/mirror-prova/mirrors/<MIRROR_ID>/sync"
sleep 20
curl -fsS -H "Authorization: Bearer $GS_TOKEN" "https://<homehub>/api/v1/repos/<owner>/mirror-prova/mirrors/<MIRROR_ID>"
```

Esito atteso: `sync` risponde `202`; poi `state` è `in_sync` e `git ls-remote` su GitHub dà lo stesso SHA di homehub.

**Esito:** la condizione 4 è provata se i passi 1–4 danno gli esiti attesi **e** in quei passi `state` non è mai `diverged`; il passo 5 deve fermarsi in `diverged` senza sovrascrivere. Se nei passi 1–4 compare `diverged` (il repo GitHub non era vuoto, o qualcuno ha spinto lì) la prova va rifatta con un repo nuovo.

**Pulizia**

```sh
curl -fsS -X DELETE -H "Authorization: Bearer $GS_TOKEN" \
  "https://<homehub>/api/v1/repos/<owner>/mirror-prova/mirrors/<MIRROR_ID>"    # 204
rm -rf /tmp/gh-prova
```

Poi su GitHub: cancella il repo di prova e **revoca** il token. Il mirror del repo vero di GitStack si crea solo nel giorno scelto per la tappa 2, con le regole di [Mirroring to GitHub](#mirroring-to-github).

### Condizione 3: prova reale di un agente con `gs` e skills (GIT-254)

**Esito, 2026-10-18: superata su homehub** (`gs version` = `sha-e984353074aeab42560042a2f79079417537819d`, GIT-249 incluso). Si è seguito [`agent-trial.md`](agent-trial.md) con `gs` scaricato da `/downloads` (CA interna verificata per impronta), `setup.sh` con un token admin, repo di prova `admin/agent-trial-homehub` e `check.sh` con 11 `PASS` su 11, `ESITO: prova superata`, exit 0.

Limiti dichiarati: la parte dell'agente l'ha eseguita un agente GalaxyLab (DevOps) via ssh con i comandi delle skills (`gs notification list`, `gs issue view/comment`, `git switch -c agent/1-wordcount-spazi`, `git commit -F` con `fixes #1`, `git push -u`, `gs notification read`), non una sessione interattiva di Claude Code: la scelta autonoma dei comandi da parte di Claude Code resta non provata. L'unione `--ff-only` l'ha fatta la persona (admin) e la issue si è chiusa da sola come `completed`.

Difetti trovati: nessuno in `gs`, skills o server. Due note: su homehub esisteva già `admin/agent-trial` con la issue #1 chiusa da una prova precedente (11/10), quindi la prova è stata rifatta con `TRIAL_REPO=agent-trial-homehub`; `gs auth status` ora stampa anche gli scope (la guida lo diceva senza, corretto in `agent-trial.md`). Il token admin di prova `agent-trial-admin` va revocato a fine prova (vedi la nota di revisione di GIT-254).

### Condizione 2: esito del 2026-10-18 (GIT-293)

**Stato: riuscita, controlli 1–7 tutti ok (2026-10-18), con un'avvertenza:** il restore è uscito con exit 70 e ha richiesto il riavvio a mano di NATS, core e gateway (difetto GIT-297). **Avvertenza superata il 2026-10-19:** con GIT-297 il restore esce con exit 0 senza passi a mano, vedi la seconda prova qui sotto (GIT-299).

- **Versione:** homehub e VM `sha-e984353074aeab42560042a2f79079417537819d`.
- **Archivio ripristinato:** `gitstack-backup-20261018T184439Z-sha-e984353074aeab42560042a2f79079417537819d.tar.gz.enc` (8 710 458 byte, cifrato AES-256-GCM, creato il 2026-10-18 alle 18:44:39 UTC da `gitstack backup --key-file /etc/gitstack/backup.key`, meno di 24 ore).
- **Cifratura (GIT-249):** i backup del timer erano non cifrati. Il board ha creato `/etc/gitstack/backup.key` (0600, root) e ha messo nel drop-in di `gitstack-backup.service` `ExecStart=/usr/local/bin/gitstack backup --key-file /etc/gitstack/backup.key`. La chiave va conservata anche fuori da homehub: senza, gli archivi non si aprono.
- **VM:** `gitstack-test-vm` (Hyper-V, Ubuntu 24.04.5, 4 vCPU, 8 GB), riportata al checkpoint `clean` del 28/9 prima della prova (nessun GitStack, nessun k3s), poi installata con `install.sh` dello stesso commit, `GITSTACK_REF=<commit>` e `--image-tag sha-e984353…`; `gitstack status` sano prima del restore. Con `GITSTACK_REF` mancante l'installer risolve il chart da `main` e si ferma con «chart firmato non trovato».
- **Restore:** `gitstack restore --key-file … <archivio>` ha eseguito tutti i passi (verifica archivio, database in una transazione, repo, allegati, Secret, 7 file di configurazione, CA e certificato per l'IP della VM) ma è uscito con **exit 70**: «gateway non è tornato pronto dopo il restore». **Difetto trovato** (visto nelle due prove): il restore sostituisce i Secret, tra cui le credenziali di NATS, ma non riavvia il `StatefulSet` `gitstack-nats`, che continua con la password vecchia; `core` va in CrashLoop con `nats: Authorization Violation` e il gateway non diventa pronto. Rimedio usato: `kubectl rollout restart statefulset/gitstack-nats`, poi `deploy/gitstack-core` e `deploy/gitstack-gateway`. Il restore va corretto perché riavvii anche NATS (GIT-297).

| # | Controllo | Esito |
|---|---|---|
| 1 | Servizi sani | ok: `Stato: sano`, `exit=0`, tutti i servizi 1/1 (dopo il riavvio di NATS) |
| 2 | API raggiungibile | ok: `https://172.29.132.37/api/healthz` → `200` |
| 3 | Repo di homehub | ok: `admin/agent-trial`, `admin/agent-trial-homehub`, `gitstack/gitstack-mirror` (il mirror in pull), come su homehub |
| 4 | Stesso storico | ok: `git ls-remote` su `refs/heads/main` e tag byte per byte identico su VM e homehub per i tre repo (compresi tutti i tag `sha-…` di `gitstack/gitstack-mirror`) |
| 5 | Issues | ok: `admin/agent-trial` e `admin/agent-trial-homehub` hanno la stessa issue #1 (chiusa); `gitstack/gitstack-mirror` nessuna, come su homehub (via API: il `gs` dell'host dava 410, vecchio) |
| 6 | Login | ok, fatto dal board il 2026-10-18: login da browser su `https://172.29.132.37/` come `admin` con la password di homehub riuscito, repo attesi presenti. (L'agente non conosce la password: col Secret iniziale il login dava 401, perché su homehub è stata cambiata) |
| 7 | Scrittura | ok: clone di `admin/agent-trial-homehub` dalla VM, ramo `prova-ripristino-GIT-293`, push con exit 0; homehub invariato |

### Condizione 2: seconda prova del 2026-10-19 (GIT-299)

**Stato: riuscita, controlli 1–7 tutti ok, senza passi a mano.** L'avvertenza della prova del 2026-10-18 (exit 70 e riavvio a mano di NATS, core e gateway) è **superata**: da GIT-297 (MR-247, `2b16467`) `gitstack restore` riavvia NATS dopo il ripristino dei Secret.

- **Versione:** homehub e VM `sha-d3b2b2d25a446940bed6277ed9d2fcc73800d761` (`d3b2b2d`, contiene `2b16467`). Per portare homehub a questa versione il board ha fatto `gitstack upgrade` (è servito installare il cosign pinnato in `/usr/local/bin/gitstack-cosign`, che su homehub mancava perché installato prima di V5).
- **Archivio ripristinato:** `gitstack-backup-20261019T000524Z-sha-d3b2b2d25a446940bed6277ed9d2fcc73800d761.tar.gz.enc` (9 735 680 byte, SHA-256 `7c4dd979b89bad37563adb28119059847db7a7fb4a0bba6e1bf89709e3f9c51a`, cifrato, creato il 2026-10-19 alle 00:05:24 UTC, meno di 24 ore).
- **Cartella dei backup di homehub:** `/srv/backup/gitstack`, non il default `/var/backups/gitstack` (per il passo 1 usa il valore di `backup.destination` della tua installazione).
- **VM:** `gitstack-test-vm` riportata al checkpoint `clean`, installata con `install.sh` dello stesso commit (`GITSTACK_REF=d3b2b2d…`, `--image-tag sha-d3b2b2d…`, firme cosign verificate), `gitstack status` sano prima del restore.
- **Restore:** `sudo gitstack restore --key-file /tmp/backup.key <archivio>` alle 00:11 UTC, **exit 0**, nessun `rollout restart` a mano. Righe di log nell'ordine documentato: `archivio verificato: versione sha-d3b2b2d… del 2026-10-19T00:05:24Z, 14 file`, `fermo i servizi: core, gateway, git, identity`, database, repo, allegati, Secret, `ripristinati 7 file di configurazione…`, **`riavvio NATS per le credenziali ripristinate`**, riallineo di CA e certificato, `riavvio i servizi`, `riavvio il web per servire la CA ripristinata`, attesa dell'Ingress, `Restore completato: database, repo, allegati, Secret e configurazione ripristinati.`

| # | Controllo | Esito |
|---|---|---|
| 1 | Servizi sani | ok: `Stato: sano`, tutti i servizi 1/1 pronti |
| 2 | API raggiungibile | ok: `https://172.29.132.37/api/healthz` → `200` |
| 3 | Repo di homehub | ok: `admin/agent-trial`, `admin/agent-trial-homehub`, `gitstack/gitstack-mirror` |
| 4 | Stesso storico | ok per `admin/agent-trial` (hash di `ls-remote` su `main` e tag `9def1921…d254`) e `admin/agent-trial-homehub` (`cfc42f19…b2cc`), uguali su VM e homehub. `gitstack/gitstack-mirror` (72 ref sulla VM, `main` = `d3b2b2d`) confrontato solo a titolo informativo: il mirror in pull può avere sincronizzato tag dopo il backup |
| 5 | Issues | ok: 1 issue (chiusa) in `admin/agent-trial` e in `admin/agent-trial-homehub`, uguale su homehub; 0 in `gitstack/gitstack-mirror` |
| 6 | Login | ok, fatto dal board il 2026-10-19: login da browser su `https://172.29.132.37/` come `admin` |
| 7 | Scrittura | ok: clone di `admin/agent-trial-homehub` dalla VM, ramo `prova-ripristino-GIT-299`, push con exit 0; homehub invariato |

Note: l'agente non conosce la password dell'admin di homehub, quindi per i controlli 3–5 e 7 ha inserito nel solo database della VM un token di prova (`git299-prova`, scadenza 1 giorno), revocato a fine prova; il confronto con homehub (controlli 4–5) l'ha fatto il board con un token temporaneo di homehub, poi revocato.

**Pulizia.** Chiave e archivio copiati sull'host e sulla VM, e la cartella `~/trial/GIT-299` di homehub (chiave, archivio, cosign scaricato, `prep.sh`, log), rimossi con `shred`. Resta a homehub l'archivio nella cartella dei backup `/srv/backup/gitstack`: è un backup vero, lo toglie la rotazione. Resta al board la VM `gitstack-test-vm`, accesa, da spegnere o da riportare a `clean` perché contiene una copia dei dati e dei Secret di homehub.

### Condizione 4: esito del 2026-10-18 (GIT-293)

**Riuscita.** Repo GitHub di prova vuoto e privato `fathorMB/gitstack-mirror-prova`, fine-grained token con solo *Contents: Read and write* su quel repo; su homehub repo di prova `admin/mirror-prova` (non il repo di GitStack).

| Passo | Esito |
|---|---|
| 1. Mirror creato | `201`, `hasToken: true`, id `ea1b225e-353a-4abb-9421-285ef404b00f`, nessun token nella risposta |
| 2. Primo push | `in_sync` in meno di 2 s; `lastPushed`: `refs/heads/main` e `refs/tags/v0.0.1` = `f1d8d8282beeb58a3350bc7cbba06f2ef52f7b68`, `lastError` nullo |
| 3. Stessi SHA | `git ls-remote` su GitHub e su homehub: identici per `main` e `v0.0.1` |
| 4. Push successivo | secondo commit `3cb8399a76d6e74b6c24e273e3ec886f2e8a3bd3`: `in_sync`, arrivato su GitHub come fast-forward, nessun `diverged` |
| 5. Divergenza | commit diretto su GitHub `a8e68f2ccf28e05dad4447064d2af8a092ec2b82`, poi terzo commit su homehub `a372c06a…`: `state: diverged`, `lastError`: «la destinazione ha una storia diversa, nessuna sovrascrittura: refs/heads/main: rejected (fetch first)». Sulla esecuzione `outcome: diverged`: tag `up-to-date`, `main` `rejected`. `git ls-remote` su GitHub resta `a8e68f2c…`: nulla è stato sovrascritto |
| 6. Ritorno a `in_sync` | non provato (facoltativo) |

Pulizia fatta: mirror cancellato (`204`), copie di chiave, archivio e token di prova rimossi con `shred` da homehub, VM e host. Fatto dal board alle 21:46 UTC: fine-grained token GitHub revocato, repo GitHub di prova cancellato, token `git293-prova` di GitStack revocato, `admin/mirror-prova` su homehub cancellato. La VM `gitstack-test-vm` è stata poi riusata per la seconda prova (GIT-299), vedi sopra.

## Registro della tappa 1 di V7 (homehub)

Registro del dogfooding: cosa è stato fatto su homehub (GIT-249, 18/10/2026, orari UTC). Ogni riga è un fatto verificato in quel giorno.

| Passo | Esito |
|---|---|
| Stato iniziale | `sha-e7087078776db0b98df12080997c1b15cc2325a8`, servizi sani |
| Backup giornaliero | Girava già: `gitstack-backup.timer` attivo (02:00:00 ogni giorno), 7 archivi da ~39 KB in `/srv/backup/gitstack`. I 316K di `/srv/backup` sono la dimensione vera dei backup (database e repo piccoli), non un backup mancante. I vecchi sono ruotati da `gitstack backup` stesso (si tengono gli ultimi 7). Gli archivi sono **non cifrati** (root-only 0600): manca `--key-file` |
| Backup manuale prima dell'upgrade | `sudo gitstack backup` alle 08:05:36: `gitstack-backup-20261018T080536Z-sha-e7087078…tar.gz`, finestra di sola lettura 1,4 s |
| Upgrade | `sudo gitstack upgrade --dry-run --to e984353` (8 immagini, binario con checksum, chart: ok), poi `sudo gitstack upgrade --to e984353`, exit 0: `sha-e708707…` → `sha-e984353074aeab42560042a2f79079417537819d`, tutti i servizi sani, backup preventivo `…T082001Z…`. `/api/v1/meta` risponde `server_version: sha-e984353…`. Il main più recente (01bca88) aveva la CI rossa (bind: address already in use nello stackitest di core) e senza immagini pubblicate, quindi la destinazione è e984353 (CI, api-contract, smoke, docs verdi) |
| Firme | L'upgrade da e708707 lo ha fatto il **binario vecchio**, che non verifica le firme cosign (arrivano con il binario nuovo): non verificate in quel passo. Anche il secondo `gitstack upgrade` (GitStack già a e984353, ramo «già alla versione») non controlla le immagini. Verificate quindi a mano il 18/10, in sola lettura, con `cosign verify --certificate-identity-regexp '^https://github\.com/fathorMB/GitStack/\.github/workflows/ci\.yml@refs/heads/main$' --certificate-oidc-issuer https://token.actions.githubusercontent.com ghcr.io/fathormb/gitstack-<svc>:sha-e984353074aeab42560042a2f79079417537819d` (workflow sha della firma = e984353…), tutte e cinque ok, e confrontate col digest in esecuzione (`kubectl get pods -n default -o jsonpath` su `imageID`): `core` `sha256:3b856d76c8dced0c6e2a60153da842c1c3653a8f1574a3c004e8713cb74274fb`, `gateway` `sha256:f985190f5928e3b0236a2a7ab72a1a72169f9891589fde4dbbf56acd4c48d376`, `identity` `sha256:7214e0d76eac135b95c5dc67831bd5614d143c34f5b37fe6ff57ac00a68f1332`, `git` `sha256:9fd85bc46d69797c2acbccb7b628df704d3988e24fc19e477b02dbaed7713c4f`, `web` `sha256:6b85dfb1c4d552715f9956a8fb23117b0ee91f266edf38fc2f1ad87bdba5fb62`: ogni digest verificato è quello che gira. Non firmate perché di terzi: postgres:16-alpine, nats:2.10-alpine, busybox:1.36. Binario `/usr/local/bin/gitstack`: SHA-256 `363095eb5bbfad121422cde8e545b1791088f1734be94fbc847429c766490089`, uguale a quello della release `sha-e984353…` e a `gitstack-linux-amd64.sha256`; `cosign verify-blob --bundle gitstack-linux-amd64.sigstore.json` (stessa identità e issuer) sul file della release: `Verified OK`. Il prossimo upgrade a una versione diversa, col binario nuovo, le verifica da sé |
| F2 (GIT-228) | `gitstack upgrade` col binario nuovo **ha fallito due volte** (exit 70, unità k3s rimessa com'era, servizi sani) con `secret-encrypt error ID …: missing annotation on node homehub`: `k3s secrets-encrypt status` girava prima che k3s scrivesse le annotazioni del nodo. Difetto corretto sul ramo (`admin/internal/upgrade/harden.go`: `status` si riprova fino a 2 minuti; test `TestUpgrade_HardeningStatusRiprovato`, `TestUpgrade_HardeningStatusSempreInErrore_RipristinaUnita`). Su homehub la procedura è stata applicata a mano: unità `k3s.service` con `--secrets-encryption` e `600` (copia in `/root/k3s.service.pre-hardening-GIT249`), `daemon-reload`, `restart`, attesa di `status`, `k3s secrets-encrypt rotate-keys`. Esito: `Encryption Status: Enabled`, `Current Rotation Stage: reencrypt_finished` |
| F1 (GIT-227) | `/etc/rancher/k3s/k3s.yaml` a `600` dopo il riavvio; `~/.kube/config` di moreno rinnovato (copia di k3s.yaml, `600`; il vecchio è `config.scaduto-20261018`) e `kubectl get nodes` risponde |
| Segreti per chiamante (GIT-235) | Il secondo `gitstack upgrade` ha cancellato `gitstack-identity-service` e lasciato `gitstack-service-secrets` (gateway, core, git). Servizi sani |
| Mirror in pull di `fathorMB/GitStack` | Creato dal board dalla UI (Importa da URL, «Mantieni sincronizzato») il 18/10 alle 14:39 UTC come `gitstack/gitstack-mirror` (non `GitStack`: il vecchio repo è nel cestino dell'organizzazione fino al 25/10 e il nome è occupato, GIT-257). Origine `https://github.com/fathorMB/GitStack.git`, senza credenziali, `intervalSeconds: 3600`. Primo sync riuscito alle 14:39:52 UTC: stato `in_sync`, nessun errore, prossimo giro 15:39:52. Verifica dei ref sul disco del servizio git: 67 ref (branch e tag) identici a `git ls-remote` di GitHub, `main` a `1c78ff42e8c2552799ea2197d08ad3694c34a5e0`. Il login dal browser in HTTPS è da confermare dal board (l'agente non ha un browser). La condizione 1 della tappa 2 (2 settimane di mirror senza errori) è stata tolta dal board il 18/10: questo sync non fa partire nessun conteggio |

## Dogfooding della CI di GitStack su homehub (v2, GIT-291)

Questa sezione dice cosa è stato provato prima di portare la CI su homehub, cosa resta da fare al board con un runbook e dove si scrivono i risultati con i link ai run. I passi su homehub li fa il board: nessun agente ha accesso a homehub.

### Provato prima (senza homehub)

| Cosa | Dove | Esito |
|---|---|---|
| `gs workflow list`, `gs workflow run -f`, `gs run watch --exit-status` (uscita 0 e 1), `gs run view --log-failed`, `gs release create/list/upload/download` contro core, gateway, identity e git veri | `TestGsCI_RunWatchViewWorkflowReleaseSulloStack` (`services/core/internal/stackitest/gs_ci_integration_test.go`, `-tags integration`) | verde in locale (Windows, Docker Desktop) il 2026-10-25; gira anche nel job `go` di `ci.yml` |
| Stesso ciclo con un workflow vero nel pod del runner integrato, immagine OCI nel registry, tag `v0.0.0-test.1` e release | passo e12 di `deploy/test-vm/e2e.ps1` (`e2e/gs-ci-cycle.sh`) | scritto e controllato con `bash -n`, shellcheck e il parser di PowerShell; **non ancora eseguito**: `e2e.ps1` vuole l'amministratore sull'host del board |
| `actions/cache` sul runner integrato (primo run «Cache saved with key: …», secondo «Cache restored from key: …») e 501 di `upload-artifact@v4` | `deploy/ci/runner-e2e.sh`, sezione 1b, nel job `chart` di `ci.yml` | scritto e controllato con shellcheck; **non ancora eseguito** su un cluster. Si legge il risultato nel job `chart` del primo run di `ci.yml` dopo il merge |

La prova contro git vero ha trovato un difetto di core, corretto in GIT-291: `GET /repos/{owner}/{repo}/workflows` e l'avvio a mano con `--ref main` risolvevano il nome del branch con `commits/{sha}/resolve`, che per git accetta solo sha. L'elenco dei workflow usciva sempre vuoto e `gs workflow run --ref main` non trovava il branch. Ora il nome si risolve col commit in testa alla ref (`commits?ref=…&perPage=1`).

### Modifiche minime ai workflow di GitStack (previsione dell'ADR 0001, da confermare)

`act -l` legge tutti i workflow di `.github/workflows` (28 job). Nel runner integrato non girano, e vanno riscritti o spostati su un runner esterno, i punti seguenti (ADR 0001, «Quanto girano i workflow di GitStack»):

- `services:` e `docker run` (job `identity-oidc`), `docker build`, `docker/*-action`, `imagetools` (job `registry`, `images`, `chart`, `security`, `install.yml`);
- job `chart` (k3d) e `install-ubuntu-22`/`e2e` (`sudo ./deploy/install.sh`): runner esterno su una macchina vera;
- `runs-on: windows-latest` (job `pwsh`): runner esterno Windows;
- firma keyless di cosign con `id-token: write` (job `sign`, `release`): su GitStack la firma è quella del registry, con la chiave dell'istanza;
- `gh release …`/`gh pr …` con `secrets.GITHUB_TOKEN`: si sostituiscono con `gs release` e con un token personale o di agente (il token del job non apre `/repos/…/releases`);
- `actions/upload-artifact@v6` (usato in `ci.yml`, `install.yml`, `security.yml`) e `actions/cache`: l'artefatto v4+ riceve 501; si carica con `PUT /ci/v1/artifacts/{nome}` o si usa un runner esterno con un servizio di artefatti compatibile;
- `actions/deploy-pages`, `environment:` (job `docs`): solo GitHub.

Il dogfooding scrive qui, per ogni workflow, se gira com'è o con quale modifica; la prima colonna è la previsione, la seconda la scrive il board dopo il run.

| Workflow | Previsione | Esito su homehub (run) |
|---|---|---|
| `docs.yml` | build del sito sì; `deploy-pages` no | da eseguire |
| `api-contract.yml` | sì (go e redocly nel pod, rete aperta verso internet) | da eseguire |
| `ci.yml` (job `go`, `web`, `lint`) | sì con l'immagine del runner e la toolchain; `upload-artifact@v6` da cambiare | da eseguire |
| `ci.yml` (job `chart`, `registry`, `images`, `release`) | no: Docker, k3d, firma keyless | da eseguire (runner esterno o riscrittura) |
| `smoke.yml`, `install.yml`, `security.yml` | no nel pod (Docker, k3s, Trivy sulle immagini) | da eseguire |

### Runbook per il board

Prima: `ci_status` di main verde (al 2026-10-25 il run `api-contract` di `c01e14b` era rosso: guardarlo), CI accesa su homehub (`sudo ./deploy/install.sh --ci --ci-build`, oppure `sudo gitstack upgrade --to <versione> --set ci.enabled=true --set ci.build.enabled=true`), un token del board (lezione dell'azienda: serve anche la CA interna, `--cacert`/`http.sslCAInfo`).

1. Repo di prova su homehub (`admin/ci-dogfood`) con una copia dei workflow di GitStack: `git push` da una copia locale, con la CA e `Basic gs:<token>`.
2. Cache: due `gs workflow run cache.yml --ref main` di fila (il workflow è in `deploy/ci/runner-e2e.sh`, sezione 1b). Il log del job `cache` deve avere «Cache saved with key: e2e-cache-v1» nel primo run e «Cache restored from key: e2e-cache-v1» nel secondo; il job `artefatto` deve fallire col messaggio «GitStack non supporta actions/upload-artifact@v4…».
3. Workflow di GitStack: uno alla volta con `gs workflow run <file> --ref main` e `gs run watch --exit-status`; per ogni job rosso, `gs run view --log-failed` e la modifica minima fatta.
4. Release di prova: tag `v0.0.0-test.N` sul repo di prova e `gs release create --tag v0.0.0-test.N --generate-notes --prerelease`. Le immagini si pubblicano nel registry di homehub e `gs package list` mostra `signature: verified` (il registry firma da solo). **Limite noto:** un workflow oggi non può costruire un'immagine nel pod (il tipo di job `build` del chart non è collegato a `runs-on:`) e non può creare la release col token del job: build e release del passo 4 si fanno da un runner esterno o dal terminale.
5. Un riepilogo per controllo, una riga ciascuno (repo, atteso, trovato, ok/ko), senza espressioni `--jq` con spazi.

### Risultati (li scrive il board)

| Voce | Valore |
|---|---|
| Data del giro su homehub | da eseguire |
| Run della cache: primo run («Cache saved with key: …») | da eseguire (link al run) |
| Run della cache: secondo run («Cache restored from key: …») | da eseguire (link al run) |
| `upload-artifact@v4`: messaggio 501 nel log | da eseguire (link al run) |
| Workflow di GitStack che girano com'è | da eseguire |
| Workflow di GitStack con modifica minima (quale) | da eseguire |
| Release di prova `v0.0.0-test.N` e immagini firmate | da eseguire (link alla release e a `gs package list`) |

## Registro del dogfooding (dalla tappa 2 di V7)

Il dogfooding della v1.0 dura almeno **2 settimane dalla tappa 2** (decisione del board del 18/10/2026). Lo compila il board: la data di inizio è il giorno della tappa 2; i fatti si scrivono quando succedono, con data e orari UTC.

| Voce | Valore |
|---|---|
| Data di inizio (tappa 2) | da compilare |
| Data di fine prevista (inizio + 14 giorni) | da compilare |
| Perdite di dati | da compilare (nessuna / descrizione) |
| Restore eseguito su homehub o su una VM da un backup di homehub (data, archivio, esito) | da compilare |
| Aggiornamento rc.N → rc.N+1 su homehub con `gitstack upgrade` (data, versioni, esito) | da compilare |
| Bug critici o alti aperti (controllo a fine periodo) | da compilare |

Il periodo è valido solo se tutti i quattro criteri valgono: nessuna perdita di dati, almeno un restore, almeno un upgrade rc.N → rc.N+1, nessun bug critico o alto aperto.

---

*Guida operativa — install.sh (T-09 di M-08 [c_8458909a21d9035f]),
decisioni D17–D19 [c_1d1d61aca3dea601]*
