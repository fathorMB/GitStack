---
id: corporate-network
title: Corporate network
---

For a server with no direct way out: an HTTP proxy, internal image and
download mirrors, a corporate CA. Every option is optional, they combine, and
the installer remembers them (section `network:` of `/etc/gitstack/config.yaml`).

```sh
sudo ./deploy/install.sh \
  --http-proxy http://proxy.corp:3128 --no-proxy registry.corp,git.corp \
  --registry-mirror https://registry.corp:5000 \
  --registry-username puller --registry-password-file /root/registry.pw \
  --download-mirror https://files.corp/gitstack \
  --ca-bundle /root/corp-ca.pem
```

| Option | What it does |
|---|---|
| `--http-proxy`, `--https-proxy`, `--no-proxy` | Used by the installer's downloads, by k3s and containerd, and by the GitStack pods (webhooks, mirrors, OIDC, SMTP). Cluster networks never go through the proxy. |
| `--registry-mirror` | Every image, including k3s system images, is requested from this mirror first. It must serve images under their original name without the registry host. |
| `--download-mirror` | Where k3s, Helm and the `gitstack` binary are downloaded from, in a fixed layout (below). |
| `--ca-bundle` | A corporate CA, trusted by the system, by containerd and by the pods. |

The download mirror has this layout: `k3s/install.sh`,
`k3s/releases/download/<version>/...`, `helm/helm-<version>-linux-amd64.tar.gz`,
`gitstack/<tag>/gitstack-linux-amd64` with its `.sha256` and `.sigstore.json`,
the signed chart, and `cosign/<version>/cosign-linux-amd64`. The exact list is in
the [Operations guide](./operations.md#corporate-network-proxy-mirrors-and-ca).

## Signatures on a mirror

Signature rules do not change on a mirror: the mirror must carry the
`.sigstore.json` bundles and the image signatures (a pull-through cache of
`ghcr.io` has them). A mirror without signatures is refused unless you state
the exception explicitly with `--allow-unsigned-mirrors`; the exception is
saved, reported by `gitstack status` and shown in Admin · System. See
[Verify signatures](./verify-signatures.md).

## Before and after

- **Preflight.** The installer checks that the proxy and the mirrors answer
  and lists what does not, before touching the system.
- **Change later.** `sudo gitstack config set network ...` changes the same
  settings, checks reachability first and restarts k3s only if needed;
  an empty value removes a setting, `--clear` removes everything.
- **Upgrades** keep the network settings and use the proxy and the mirror
  (also for k3s).

## Which addresses webhooks and mirrors may reach

Users give GitStack addresses (webhooks, mirrors), so every outgoing
connection goes through an egress policy. Loopback, link-local, cluster
networks, the node's own addresses and the Kubernetes ports are always
blocked. Other LAN hosts are reachable by default; to close the list, state
what is allowed:

```sh
sudo ./deploy/install.sh --egress-allow 192.168.1.0/24,ci.example.com
```

With a non-empty list only what it covers is allowed, public addresses
included. Like any `--set`, give it again at every upgrade. See
[Egress policy and the LAN](./operations.md#egress-policy-and-the-lan).

A mirror whose remote uses a private CA needs that CA in a ConfigMap
referenced by `git.mirror.caConfigMap`
([details](./operations.md#destinations-on-a-private-network-or-with-a-private-ca)).
