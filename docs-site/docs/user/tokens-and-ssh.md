---
id: tokens-and-ssh
title: Tokens and SSH keys
---

There are two ways for Git to authenticate: a personal token over HTTPS, or
an SSH key. Web passwords and web sessions never open Git access.

## Personal tokens

Create a token in **Settings → Tokens**. It starts with `gst_` and is shown
once. A token has scopes and an expiry date, and can be revoked at any time.

| Use | Scope | Role needed on the repository |
|---|---|---|
| clone, fetch, pull | `read:resource` | `read` |
| push | `write:resource` | `write` |
| settings, archive, delete | `write:resource` | `admin` |

Tokens are also what `gs` and the API use (`Authorization: Bearer gst_...`).

### Git over HTTPS

The user name is your own and the password is the token. Store it once with a
credential helper:

```sh
git clone https://HOST/OWNER/REPO.git
git config --global credential.helper 'cache --timeout=3600'   # Linux, in memory
git config --global credential.helper store                    # Linux, on disk
gs auth setup-git                                              # use gs as the helper
```

Git Credential Manager (Windows, macOS) already remembers it. Avoid putting
the token in the URL: it ends up in `.git/config` and in the shell history.

If the instance uses an internal CA, install it first
([trust the CA](../admin/https-and-ca.md#trust-the-internal-ca-on-clients)).

## SSH keys

Add your public key in **Settings → SSH keys**. A key gives the access of its
user, with the user's roles. Only key authentication is accepted, with the
login `git`:

```sh
git clone ssh://git@HOST:2222/OWNER/REPO.git
```

The port is 2222 by default (the short `git@HOST:OWNER/REPO` form only works
when the port is 22). The clone menu of each repository shows the exact
addresses for your instance.

## Errors you may see

| Answer | Meaning |
|---|---|
| 401 | Missing, wrong, expired or revoked token |
| 404 | The repository does not exist, or you cannot read it (the same answer, on purpose) |
| 403 | You can read it, but the token lacks `write:resource` or you have no `write` role; or the repository is archived |
