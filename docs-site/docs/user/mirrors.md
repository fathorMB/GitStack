---
id: mirrors
title: Mirrors and import
---

GitStack can pull a repository from another server (a read-only copy, or a
one-time import) and can push your repository to another server. Both work
over HTTPS only, with credentials that are stored encrypted and never shown
again.

## Pull mirror and import

A **pull mirror** keeps a read-only copy of an external repository, for
example one on GitHub: a backup on your own machine, or the first step of
leaving GitHub.

In the web UI choose **New repository → Import from URL**, give the
`https://` address and pick:

- **Keep in sync**: a pull mirror, updated every 10 minutes by default
  (between 5 minutes and 24 hours). **Sync now** updates immediately.
- **Copy once**: all branches and tags are copied one time, then the
  credentials are deleted and the repository is a normal, writable one with no
  link to the origin.

A public origin needs no credentials. For a private GitHub repository give
your user name and a fine-grained token with **Contents: Read-only** on that
repository only.

What a pull mirror does:

- It copies all branches and tags and **never deletes or overwrites**. If the
  origin rewrites or deletes a ref, GitStack keeps its version, marks the ref
  **diverged** and notifies the repository admins.
- While active, the code is read-only (pushes are rejected and name the
  origin); issues, labels and milestones work as usual.
- Only Git content is copied: not issues, pull requests, releases or LFS.
- **Stop** (Settings → Pull mirror) makes the repository normal and writable;
  this is how a copy becomes the new home after a migration.

States are `pending`, `syncing`, `in_sync`, `error` (retried with growing
delays), `paused` and `stopped`.

## Push mirrors

A **push mirror** sends the default branch and all tags of a repository to
another Git server, for example a public copy on GitHub. It pushes after every
push to the default branch or to a tag, and when you press **Sync now**.

1. On the destination create an **empty** repository (no README or licence).
2. Create a token that can write there; on GitHub a fine-grained token with
   **Contents: Read and write** on that single repository.
3. In GitStack open the repository's **Settings → Mirrors → Add mirror** and
   give the URL (`https://github.com/OWNER/REPO.git`), the user name and the
   token. You need the `admin` role.

The mirror is healthy when its state is **In sync**. It **never forces**: if
the remote has a different history, or a tag points elsewhere, the mirror
stops as **Diverged** and notifies the admins. Fix the difference on the side
that is wrong, then press **Sync now**. Other branches are not mirrored.

Both kinds of mirror are subject to the instance's egress policy: your
administrator may need to allow the destination
([Corporate network](../admin/corporate-network.md#which-addresses-webhooks-and-mirrors-may-reach)).

Reference with every state, retry and recovery step:
[Operations guide: Mirror](../admin/operations.md#mirror) and
[Pull mirror](../admin/operations.md#pull-mirror).
