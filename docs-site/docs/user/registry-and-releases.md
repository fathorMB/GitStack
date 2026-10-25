---
id: registry-and-releases
title: Container registry and releases
---

## Container registry

Every repository can publish OCI images to GitStack with `docker`, `podman`,
`crane` or from the CI. Images are named `<host>/<owner>/<repo>[/<name>]`:

```sh
echo "$GS_TOKEN" | docker login gitstack.example.com -u token --password-stdin
docker tag app:1 gitstack.example.com/alice/app:1
docker push gitstack.example.com/alice/app:1
```

The password is a personal or agent token with the `registry` scopes
(`read:registry` to pull, `write:registry` to push, `admin:registry` to
delete), or the token of a CI job. You also need the matching role on the
repository: `read` to pull, `write` to push, `admin` to delete. There is no
anonymous access.

```sh
gs package list -R alice/app             # images, tags, size, who pushed, signature
gs package delete alice/app --tag 1 --yes -R alice/app   # without --tag the whole image
```

The Packages tab of the repository shows the same list, with the cleanup rules
for admins.

### Cleanup, protected tags and quotas

- Images without a tag are removed after 7 days; an image with a tag is never
  removed on its own.
- Optional rules per repository: keep the last N tags, or remove `sha-*` tags
  older than X days.
- Protected tags (for example `v*`) cannot be overwritten or deleted.
- An owner has a quota; GitStack warns above 80 %.
- Images are **not** in backups unless the repository sets "include in backup".
  After a restore without images, rebuild them from the CI.
- Images pushed by the CI are signed with the instance key; signatures and SBOMs
  are stored next to the image.

## Releases

A release is a Git tag with notes and files, like on GitHub. Anyone with `write`
can create one from the Releases tab, the API or the terminal:

```sh
git tag v1.2.0 && git push origin v1.2.0
gs release create -R alice/app --tag v1.2.0 --name "1.2.0" --generate-notes
gs release upload <release-id> ./app-linux-amd64 -R alice/app     # prints the SHA-256
gs release list -R alice/app
gs release download <release-id> <asset-id> -R alice/app -o app
```

`--generate-notes` drafts the notes from the commits and the merged pull
requests since the previous release; `--draft` and `--prerelease` set those
flags. Files are up to 2 GB, each with its SHA-256, and `gs release download`
refuses a file whose checksum does not match. A webhook `release` event is sent.
Release notes are included in backups; attached files are not (like images).

A workflow cannot create a release with its job token: the token is valid for
the CI gateway and the registry only. Create the release with `gs release` and
a token of a person or of an agent.
