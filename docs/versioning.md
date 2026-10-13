# Versioning Policy

GitStack follows [Semantic Versioning (SemVer)](https://semver.org/) for all
releases. Versions use the format `MAJOR.MINOR.PATCH`.

## Release Cadence

- **MAJOR** versions denote breaking changes.
- **MINOR** versions add features in a backward-compatible manner.
- **PATCH** versions include backward-compatible bug fixes. Bug fixes are
  released only for the latest minor version.

## Updating

Users can update directly from any minor version to any subsequent minor
version without going through intermediate releases. Patch updates are always
forward-compatible.

For example, upgrading from `v1.2.x` directly to `v1.4.x` is supported, as
well as upgrading from `v1.0.x` to `v1.3.x`.

## Release Candidates

Release candidates are tagged as `vMAJOR.MINOR.PATCH-rc.N` (for example
`v1.0.0-rc.1`, `v1.0.0-rc.2`). Release candidates are feature-complete but
may contain bugs; they are intended for testing before a final release.

The `v1.0.0-rc.1` tag denotes the first release candidate for the v1.0
release, `v1.0.0-rc.2` the second, and `v1.0.0-rc.N` the Nth. Each
candidate is a full build of the product and is tested against the same CI
gates as stable releases.

## Security Patches

Security patches are released for the latest minor version and for the
previous minor version for 6 months after the release of the next minor
version. See [`SECURITY.md`](../SECURITY.md) for details on supported versions.
