---
id: system
title: Admin · System
---

**Admin · System** is the web page where an installation administrator sees
the state of the machine and of the installation without a shell. It is
visible to installation administrators only.

The page shows what the host publishes. The `gitstack` command on the server
writes the information (the installer, `gitstack upgrade` and `gitstack backup`
do it by themselves); core only reads it, so the page never invents a value.
If the host has not published yet, the page says so; run
`sudo gitstack system-info publish`.

| Card | What it shows |
|---|---|
| Header and notices | GitStack and k3s versions; a notice when a k3s patch or minor update is available; a **supply-chain warning** when the unsigned-mirror exception is active |
| Access | Host, HTTPS mode and SSH port |
| Services | Readiness of gateway, identity, git, core, web, PostgreSQL and NATS |
| Machine | CPU, RAM and disk against the requirements profile |
| Database | Bundled or external PostgreSQL, with host, database and version |
| Network | Proxy, registry and download mirrors, corporate CA, egress policy |
| Backup | Destination, retention, last result, and the S3 upload state |

The same information, as text or JSON, comes from `sudo gitstack status
[--json]`. What to do when something is red is explained in
[Upgrade](./upgrade.md), [Backup and restore](./backup-restore.md) and
[Corporate network](./corporate-network.md).
