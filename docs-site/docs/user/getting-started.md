---
id: getting-started
title: Getting started
---

## Personal tokens

Git over HTTPS, the `gs` CLI and the API all authenticate with a **personal
token** (it starts with `gst_`). Create one from the settings of the web UI.
A token carries scopes: `read:resource` is enough to clone, fetch and pull;
`write:resource` is needed to push.

## Git over HTTPS

A repository is reachable at:

```
https://<host>/<owner>/<repo>.git
```

`<owner>` is a user or an organization. Git asks for credentials on the first
clone: the user name is your username and the password is the token.

```sh
git clone https://<host>/<owner>/<repo>.git
```

Web sessions and passwords do not open Git access; an expired or revoked token
is rejected.

## The `gs` command-line tool

`gs` works on repositories, issues, labels, milestones, notifications and
search from the terminal, and is designed for coding agents as well as people.

```sh
gs auth login --hostname <host>
gs repo list
gs issue list -R <owner>/<repo>
```

Agents and CI do not need `gs auth login`: set `GS_HOST` and `GS_TOKEN`.

Every command and flag is in the [CLI reference](../cli/index.md).
