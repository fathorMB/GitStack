# Upgrading the kit

Your project's `.lmbrain-lite/VERSION` says which kit it was created from. The app's About page shows both that version and the version the app bundles.

Pulse offers assisted upgrades when your project's kit is older than the bundled kit. Updating the app does not automatically migrate your projects.

## Upgrade from Pulse

1. Read the bundled kit's `CHANGELOG.md` for the versions between yours and the app's.
2. Open the project in the app and review the **Project kit** card on Pulse. It shows the versions, the number of files to realign, and whether local edits were detected or could not be verified against a recorded baseline.
3. If the plan has no modified or unverified files, choose **Migrate now**, then **Yes, migrate the kit**. The app prepares the upgraded kit before replacing the live directory, keeps the previous kit in `.lmbrain-lite-backup-<timestamp>` beside it, and updates `VERSION` and the baseline after applying the upgrade.
4. If files have local edits, have no baseline, or the preview cannot be built, choose **Copy migration prompt** and paste it into an agent session. Compare the files and preserve your customizations before applying changes; do not treat unverified files as safe to overwrite.

Project-owned state is preserved during the app migration. If the project's kit is newer than the bundled kit, update the app instead of downgrading the kit. If a version cannot be read, use the migration prompt to investigate before changing it.

## Manual fallback

Use these steps when carrying out an upgrade manually or with an agent. Back up the project's kit first.

1. Read `CHANGELOG.md` in the bundled kit for the versions between yours and the app's.
2. Copy the kit-owned files you did not personalize over your project's copies:
   `AGENT.md`, `OPERATOR.md`, `README.md`, `UPGRADING.md`, `CHANGELOG.md`, `templates/*`, `mcp/README.md`, `milestones/README.md`, `knowledge/README.md`, `skills/README.md`, `design/README.md`, `reports/README.md`.
3. Never overwrite: `PROJECT.md`, `milestones/M-*.md`, `LOG.md`, `NOTE.md`, `knowledge/*` pages, `skills/*` runbooks, `reports/lmbrain-kit-feedback.md`, `HARNESSES.json`.
4. Set `VERSION` to the bundled kit version.
5. Open the workspace in the app. Pulse shows a diagnostic if something is missing.

The bundled kit lives next to the installed app under `kit/.lmbrain-lite/`. On Windows: `%LOCALAPPDATA%\Programs\LMBrain Lite\kit\.lmbrain-lite\` for the per-user installer.
