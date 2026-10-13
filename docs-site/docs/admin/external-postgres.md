---
id: external-postgres
title: External PostgreSQL
---

By default GitStack runs its own PostgreSQL inside the cluster. To use your
server instead, PostgreSQL **15, 16 or 17** is supported.

## 1. The DBA prepares the database, once

`deploy/postgres-prepare.sql` creates, idempotently, a `gitstack` user
(`LOGIN CREATEROLE`, never superuser) and the `gitstack` database it owns:

```sh
export GITSTACK_DB_PASSWORD='a-password'   # letters, digits and - . _ ~ only
psql "postgres://postgres@db.example.com:5432/postgres?sslmode=require" \
     -v ON_ERROR_STOP=1 -f deploy/postgres-prepare.sql
```

`GITSTACK_DB_NAME` and `GITSTACK_DB_USER` change the names. Do not revoke
`CONNECT` from `PUBLIC`: the application role GitStack creates must connect.

## 2. Install

Give the password in a file, not on the command line:

```sh
sudo ./deploy/install.sh \
  --postgres-url 'postgres://gitstack@db.example.com:5432/gitstack?sslmode=verify-full' \
  --postgres-password-file /root/gitstack-db.pw \
  --postgres-ca-file /root/db-ca.crt
```

`sslmode` defaults to `require`; the CA file is needed for `verify-ca` and
`verify-full` with a private CA. Before touching the machine the installer
checks connection, version and the right to create schemas and roles. You
can run the same check at any time (exit code `11` means unsuitable):

```sh
gitstack postgres check --url 'postgres://gitstack@db.example.com:5432/gitstack?sslmode=require' \
  --password-file /root/gitstack-db.pw
```

## What to know

- GitStack only touches its own schemas: `core`, `identity` and the
  `identity_app` role.
- Backup, restore and upgrade work as with the bundled database
  ([Backup and restore](./backup-restore.md)). A restore needs the same
  database user that owned the objects when the backup was taken.
- The database password must be URL-safe.
- Re-running `install.sh` needs the same `--postgres-*` options; without them
  it stops rather than switching to the bundled database.
- The database speaks plain TCP: proxy settings do not apply to it
  ([Corporate network](./corporate-network.md)).
- After a PostgreSQL major upgrade, change `postgres.external.client.image.tag`
  too; `gitstack status` warns when client and server differ.

Reference: [Operations guide](./operations.md#external-postgresql).
