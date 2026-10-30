---
id: index
title: Admin guide
---

This section is for whoever installs and runs a GitStack instance. The
pages follow the life of an installation.

| Step | Page |
|---|---|
| Is my machine suitable? | [Requirements and supported systems](./requirements.md) |
| Install | [Installation](./install.md) (includes WSL2 for trials) |
| HTTPS and trusting the CA | [HTTPS and certificates](./https-and-ca.md) |
| Use your own database | [External PostgreSQL](./external-postgres.md) |
| Closed network | [Corporate network](./corporate-network.md): proxy, mirrors, CA, egress |
| Let apps be public on the internet | [Public apps](./public-apps.md): port 80, ACME e-mail, Let's Encrypt limits |
| Protect the data | [Backup and restore](./backup-restore.md), including S3 |
| Keep it current | [Upgrade](./upgrade.md), k3s included |
| Trust what you run | [Verify signatures](./verify-signatures.md) |
| Look at the state | [Admin · System](./system.md) |

Reference pages, which are copies of files of the repository made at build
time and remain the single source:

- [Operations guide](./operations.md) (`docs/operations.md`): every option,
  exit code and procedure.
- [Versioning policy](./versioning.md) (`docs/versioning.md`): Semantic
  Versioning and supported upgrade paths.
- [Security policy](./security-policy.md) (`SECURITY.md`): supported versions
  and how to report a vulnerability.

The guides above are short and task-oriented and link to the reference
instead of repeating it.
