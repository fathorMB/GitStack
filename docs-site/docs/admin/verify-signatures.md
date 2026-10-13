---
id: verify-signatures
title: Verify signatures
---

GitStack signs its container images, the Helm chart and the binaries with
Sigstore cosign (keyless, through the OIDC identity of GitHub Actions) and
publishes an SPDX SBOM for each component.

## Automatic verification

`install.sh` and `gitstack upgrade` verify everything by themselves, with the
same rules as CI, and stop **before touching the system** if a signature is
missing or wrong:

| Artefact | Check |
|---|---|
| `gitstack` binary | SHA-256, and `cosign verify-blob` with the `.sigstore.json` bundle |
| Helm chart | the signed `.tgz` is downloaded and verified before it is unpacked |
| Images `gitstack-gateway/identity/git/core/web` | `cosign verify` before `helm upgrade`. PostgreSQL and NATS are third-party images and are not signed by GitStack. |

The identity is fixed to the `ci.yml` workflow of the repository, run from
`main` or from a `v*` tag, with issuer `https://token.actions.githubusercontent.com`.
The bundle is mandatory, cosign is pinned (`v2.4.0`, SHA-256 checked) and
installed as `/usr/local/bin/gitstack-cosign`.

A chart read from a local folder cannot be verified: the installer says so,
and still verifies the images.

### Exception for unsigned internal mirrors

If an internal mirror copies releases and images without their signatures,
you can allow it explicitly:

```sh
sudo ./deploy/install.sh --allow-unsigned-mirrors
sudo gitstack upgrade --allow-unsigned-mirrors     # this run only
```

This skips every cosign check (the binary SHA-256 stays mandatory), prints an
`ECCEZIONE N9 ATTIVA` warning, is stored in `config.yaml`, and makes
`gitstack status` and Admin · System show a supply-chain warning. Running
`install.sh` again without the flag removes it.

## Verify by hand

Install `cosign` and, for SBOMs, `syft`. Then, for an image of a tagged release:

```sh
cosign verify \
  --certificate-identity-regexp '^https://github.com/fathorMB/GitStack/\.github/workflows/ci\.yml@refs/tags/v.*$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/fathormb/gitstack-gateway:X.Y.Z
```

For a binary or the chart, download the file and its `.sigstore.json` from
the release and verify the blob:

```sh
cosign verify-blob \
  --certificate-identity-regexp '^https://github.com/fathorMB/GitStack/\.github/workflows/ci\.yml@refs/tags/v.*$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --bundle gitstack-linux-amd64.sigstore.json \
  gitstack-linux-amd64
```

Builds of `main` (`sha-<commit>`) are signed by the workflow on
`refs/heads/main`: use `--certificate-identity` with
`https://github.com/fathorMB/GitStack/.github/workflows/ci.yml@refs/heads/main`.
The identity is the URL of the workflow; the issuer is the OIDC provider.

SBOMs are release files named `gitstack-<component>-sbom.spdx.json`; images
also carry them as in-toto attestations (`cosign verify-attestation --type spdxjson`).
`SHA256SUMS` of a release is signed too.

Full file list and the signing flow:
[Operations: supply chain](./operations.md#v5--catena-di-fornitura-firme-e-sbom)
(that section of the guide is still in Italian).
