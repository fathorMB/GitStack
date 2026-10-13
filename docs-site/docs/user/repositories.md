---
id: repositories
title: Repositories
---

A repository lives at `https://HOST/OWNER/REPO`, and its Git address is
`https://HOST/OWNER/REPO.git`. `OWNER` is a user or an organization; users and
organizations share one namespace, so a name is unique.

## Create

In the web UI choose **New repository**, or from the terminal:

```sh
gs repo create OWNER/REPO
```

You choose the owner, the visibility, the main branch, and optionally an
initial README, a `.gitignore` template and a licence. Visibility is
**private** (only people you give a role) or **internal** (every user of the
instance can read it). There are no anonymous repositories.

Roles on a repository are `read`, `write` and `admin`. `read` lets you clone
and open issues; `write` lets you push and manage issues; `admin` manages the
settings, webhooks and mirrors.

## Browse

The web UI has the file tree, a file view with syntax highlighting, README
rendering, blame, history of a file, branches, tags, commit pages with the
diff, "Go to file" and code search. Large files can be downloaded raw, and a
branch, tag or commit can be downloaded as `.zip` or `.tar.gz`. From the
terminal: `gs browse`, `gs blame`, `gs search`.

## Clone and push

```sh
git clone https://HOST/OWNER/REPO.git            # needs a token
git clone ssh://git@HOST:2222/OWNER/REPO.git     # needs an SSH key
```

Authentication is explained in [Tokens and SSH keys](./tokens-and-ssh.md).
Pushes to the main branch may be limited by the repository's rules, and an
archived repository rejects pushes.

## Archive, delete, restore

- **Archive** makes a repository read-only (`gs repo archive`); unarchive
  brings it back.
- **Delete** (`gs repo delete`) moves it to the trash, from which it can be
  restored for 7 days (`gs repo restore`, or **Settings → Deleted
  repositories**). After that it is removed for good.

## Bring code in or send it out

- Copy or keep in sync a repository from another server:
  [Mirrors and import](./mirrors.md).
- Publish your main branch and tags to another server:
  [Push mirrors](./mirrors.md#push-mirrors).
