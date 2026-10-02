# Initialization and migration contract v1

Implemented: new-project initialization, legacy consultation, staged semantic review, confirmed migration, coding integration merges and recovery.

## Open and preview

Detect `.prisma/`, `.lmbrain-lite/` and `.lmbrain/` without modifying them. Prefer an existing valid Prisma brain. An original brain without Prisma can be consulted read-only and always receives a migration offer. Writing Prisma documentation requires migration. If both original formats exist, explicitly identify the selected source in preview; do not silently blend them.

The preview contains source inventory, copied assets, derived knowledge, proposed high-level roadmap, unresolved source conflicts, unsupported formats and the exact proposed configuration additions. The agent prepares semantic transformations; a filesystem converter alone must not invent product goals. The user may revise the draft and confirms the exact draft digest once before applying migration.

## Mapping

| Original content | Prisma destination |
| --- | --- |
| Project overview and knowledge | project summary and topic documents, with origin references |
| Design HTML/packages/assets | complete independent copy preserving relative assets |
| Lite milestone objectives and tasks | high-level product objectives, proposed in preview with source references |
| Standard roadmap and linked specs | product objectives grounded in existing documented outcomes |
| Standard specification requirements | topic knowledge with origin paths and certainty preserved |
| Standard architectural decisions | choices, rationale, alternatives and consequences; accepted versus proposed/superseded states preserved semantically |
| Coding MCP configuration and skills | retained in place; only non-conflicting Prisma integration additions |

Do not turn completed implementation tasks into new unfinished goals. Do not turn proposed or superseded architectural decisions into active confirmed choices. When evidence conflicts or is insufficient, present an open question in the draft. Derive the analysis journey from documented unresolved questions and topics needing review; do not invent resolved decisions.

Logs, review records, debts and execution configuration stay in the original brain. They may be consulted as evidence for an explicitly cited point but are not imported as Prisma work-management artifacts. The original brain remains independent and is never synchronized with Prisma.

## Apply and recover

1. Stage the draft outside the final `.prisma/` directory with a manifest of all files and hashes.
2. Capture the original brain and relevant integration-file hashes. Reject stale drafts.
3. Validate artifact IDs, links, source bytes, Design assets and role contracts; show the exact reviewable result.
4. After user confirmation, take the project lock and revalidate hashes and absence of a competing Prisma installation.
5. Apply a journaled transaction: atomically publish the brain and merge allowed integration additions. Keep backups of touched integration files until completion; recover failures to the pre-migration state.
6. Write a receipt with source formats, relative origin references, original hashes, converted artifact IDs, warnings and timestamp. Report success only after integration and brain verification succeed.

Never replace `.mcp.json`, `AGENTS.md`, `.codex/config.toml`, `.pi/mcp.json` or a skills directory wholesale. Use format-aware merging for known host configurations; preserve unrelated entries and instruction sections. A conflicting Prisma server key requires a visible decision; never overwrite it. Preserve coding instructions and existing skills byte-for-byte unless a separately confirmed Prisma-owned block is being updated. Do not auto-register an analysis role for every coding session.

New-project initialization uses the same non-overwrite and integration rules, without source conversion. Unsupported host formats remain untouched and produce actionable setup instructions. Sources and old projects are never deleted. After migration the original project files remain available, the new brain evolves independently and repeated opens use the Prisma brain.


## New project initialization

The desktop app accepts a project name and optional starting context in an existing folder chosen with its native folder picker (which can create a folder). Refuse folders already containing `.prisma/`, `.lmbrain-lite/` or `.lmbrain/`; open or migrate those instead. Keep unrelated project files in place. Stage the kit and project document under `.prisma-migration/INIT-<uuid>/`, with the same configuration preview, hashes, explicit confirmation and recovery journal used for migration.

Names are nonempty single lines up to 200 UTF-8 bytes; context is limited to 64 KiB. Initial knowledge and both roadmap collections remain empty: context does not become a confirmed decision or a product objective. Configuration conflicts block publication; choosing manual setup requires a new preview with no integration additions. Creation is bound to the current app workspace and cannot switch while sessions run. Cancellation leaves the current brain/configuration unchanged and retains the unpublished draft. Initialization is not exposed as an analysis/coding MCP tool. The receipt uses `source_format: new` with no legacy sources.
