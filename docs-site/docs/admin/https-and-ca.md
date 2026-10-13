---
id: https-and-ca
title: HTTPS and certificates
---

GitStack is installed with HTTPS by default. Traefik serves port 443 and
redirects port 80 to it, except `/downloads/ca.crt`, which stays on HTTP
because clients need it to trust HTTPS the first time.

## Modes

| Mode | Option | Certificate |
|---|---|---|
| Internal CA (default) | none | CA created by the installer, certificate for the host name and IP |
| Let's Encrypt | `--tls letsencrypt --host git.example.com [--tls-email me@example.com]` | Public, obtained and renewed by Traefik (HTTP-01) |
| Your own certificate | `--tls-cert FILE --tls-key FILE` | The PEM pair you supply |
| HTTP only | `--insecure-http` | None. Local tests only; the UI shows a warning. |

`--host` (repeatable) chooses the names in the certificate. The first one is
the public URL, which also appears in clone addresses and e-mail links.

```sh
sudo ./deploy/install.sh --host homehub.local
```

Let's Encrypt needs a public DNS name (no IP, no `.local`) and ports 80 and 443
reachable from the internet. It is implemented and covered by chart tests,
but it has not been proven with a real certificate issuance yet.

## Internal CA: where things are

Everything is in `/etc/gitstack/tls/` (root only). The CA key `ca.key` stays
**on the host** and is never stored in Kubernetes. Include
`/etc/gitstack/tls/ca.key` and `ca.crt` in your backups (`gitstack backup`
does): after a restore on a new machine, clients would otherwise have to trust
a new CA.

The installer enables `gitstack-tls-renew.timer`, which renews the server
certificate every night when it expires within 30 days or the host names
changed. Clients never need to act on a renewal: they trust the CA, valid for
10 years.

```sh
sudo gitstack-tls status         # mode, names, expiry, fingerprints
sudo gitstack-tls fingerprint    # SHA-256 fingerprint of the CA
sudo gitstack-tls ensure         # check and publish now
```

To rotate the CA, stop the timer, move the files in `/etc/gitstack/tls/`
away, run `sudo gitstack-tls ensure` and distribute the new
`/downloads/ca.crt` to every client. A custom certificate is renewed by you:
replace `custom.crt` and `custom.key` and run `sudo gitstack-tls ensure`.

## Trust the internal CA on clients

1. Download `http://HOST/downloads/ca.crt` (or copy `/etc/gitstack/tls/ca.crt`).
2. **Check the fingerprint** against the one printed by the installer or by
   `sudo gitstack-tls fingerprint`. Without this check, someone intercepting
   the first request could hand you a fake CA.

   ```sh
   openssl x509 -in ca.crt -noout -fingerprint -sha256
   ```

3. Install it:

   - **Debian/Ubuntu:** copy to `/usr/local/share/ca-certificates/gitstack-ca.crt`,
     then `sudo update-ca-certificates`.
   - **Fedora/RHEL:** copy to `/etc/pki/ca-trust/source/anchors/`, then
     `sudo update-ca-trust`.
   - **macOS:** `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ca.crt`
   - **Windows (elevated PowerShell):** `Import-Certificate -FilePath .\ca.crt -CertStoreLocation Cert:\LocalMachine\Root`
   - **Git only:** `git config --global http.sslCAInfo /path/to/ca.crt`
   - **curl:** `curl --cacert ca.crt https://HOST/api/healthz`

Firefox keeps its own store; Chrome on Linux uses the user NSS store. The
`gs` install scripts (`/install-gs.sh`, `/install-gs.ps1`) fetch and check the
CA for you when you give them the fingerprint (`GS_CA_SHA256`).

On Windows, `curl.exe` may fail with a revocation error because the internal
CA publishes no CRL: add `--ssl-no-revoke`. Git for Windows with schannel needs
`http.schannelCheckRevoke false`.

More detail, including the TLS store used for access by IP address, is in
[docs/tls.md](https://github.com/fathorMB/GitStack/blob/main/docs/tls.md) and
in [Operations: HTTPS modes](./operations.md#https-modes).
