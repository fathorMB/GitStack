---
id: backup-restore
title: Backup and restore
---

`gitstack backup` takes a coherent copy of the database, the Git
repositories, the attachments, the Kubernetes Secrets and `/etc/gitstack/`.
For a short moment `gateway` and `git` are scaled to zero, so the instance is
read-only while the copy runs.

## Back up

```sh
sudo gitstack backup                          # archive in backup.destination
sudo gitstack backup --key-file /root/gs.key  # AES-256-GCM encrypted
sudo gitstack backup --dest /mnt/backup/gitstack --retention 14
```

The archive is `gitstack-backup-<timestamp>-<version>.tar.gz` (`.enc` when
encrypted) with a `.sha256` file beside it. The default destination is
`/var/backups/gitstack` and the default retention is 7 archives.

The installer enables `gitstack-backup.timer`, which runs a backup every day
at 02:00 (change with `GITSTACK_BACKUP_TIMER_HOUR` when you re-run the
installer). Check it with `systemctl list-timers gitstack-backup.timer`.

**Keep the encryption key away from the archive**: an encrypted archive
without its key cannot be recovered. Copy archives off the machine with
`rsync` or with S3 (below).

## Backup to S3

Any S3-compatible storage works: AWS S3, MinIO, Backblaze B2, Wasabi, a NAS.
The daily backup writes the local archive first and uploads it afterwards.
Archives uploaded to S3 are **always encrypted**; a configuration without a key
file is refused.

```sh
export GITSTACK_S3_ACCESS_KEY=... GITSTACK_S3_SECRET_KEY=...
openssl rand -base64 32 | sudo tee /etc/gitstack/backup.key >/dev/null
sudo chmod 0600 /etc/gitstack/backup.key
sudo -E gitstack config set backup.s3 \
  --endpoint s3.example.com --bucket gitstack-backups \
  --prefix prod --region eu-central-1 --key-file /etc/gitstack/backup.key
```

The same can be given at install time with `--s3-endpoint`, `--s3-bucket` and
`--s3-key-file`; access keys come from the environment only. On S3 the newest
30 archives are kept by default (`backup.s3.retention`: a number, or `bucket`
to leave deletion to the bucket's lifecycle rules).

If an upload fails, the local archive stays valid, the command exits with code
`10`, `gitstack status` shows the failure and `backup.s3.notify_command` (if
set) runs. Details are in [Operations: Backup to S3](./operations.md#backup-to-s3-n7).

## Restore

A restore overwrites the database, repositories, attachments and Secrets of a
running instance of **the same GitStack version** that produced the archive;
the command refuses anything else.

```sh
sudo gitstack restore /var/backups/gitstack/gitstack-backup-....tar.gz
sudo gitstack restore --key-file /root/gs.key /var/backups/gitstack/gitstack-backup-....tar.gz.enc
sudo gitstack status        # all services ready, exit code 0
```

**On a new machine** (disaster recovery): install the same version with
`install.sh` (with the same `--s3-*` options and key file), then

```sh
sudo gitstack restore --from s3                  # newest archive on S3
sudo gitstack restore --from s3 gitstack-backup-....tar.gz.enc
```

The archive is downloaded to a private temporary folder, checked against its
`.sha256`, restored and removed. Exit codes and every option are in the
[Operations guide](./operations.md#restore).
