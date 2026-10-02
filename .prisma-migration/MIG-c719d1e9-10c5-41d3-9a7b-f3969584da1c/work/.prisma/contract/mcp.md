# MCP contract v1

Implementation target: `prisma-mcp --root <project> --role analysis|coding`. Require an explicit role. Each process binds one canonical project root and one immutable role. Missing or unknown role is an error. Do not allow tool arguments to change the role or root.

Two separate configured server entries (`prisma-analysis` and `prisma-coding`) allow host-specific sessions. The coding entry must not receive analysis tools through listing or calls. Dispatch checks permissions independently of tool listing. App analysis sessions explicitly select the analysis contract; coding sessions use CODING.md. The persistent coding configuration includes only the coding entry. The analysis entry is supplied explicitly to analysis sessions through host-specific session configuration; never register both globally for all sessions. When a host cannot isolate configurations, require a visible manual setup instead of claiming isolation. Existing MCP entries and coding skills remain untouched.

## Shared read tools

| Tool | Required input | Output |
| --- | --- | --- |
| `prisma_digest` | none | compact orientation, diagnostics and open-note summaries |
| `prisma_search` | query; optional area and limit | ranked ID/path/snippet references |
| `prisma_read` | artifact ID | full allowed artifact content, metadata and digest |
| `prisma_roadmap` | view: product or analysis | authoritative collection with topic links |
| `prisma_notes_list` | optional limit | open notes with IDs and digests |
| `prisma_source_read` | source ID | metadata, extracted text and extraction warnings |
| `prisma_design_list` | none | mockup metadata and local entry paths |

Read tools expose documentation, sources, mockups, dreams and skills, not arbitrary project files or host configuration secrets. Binary originals are exposed by a guarded app file-open action; text extraction is available to MCP. Digests include enough warning detail to prompt an expanded read.

## Coding mutation

`prisma_note_add(title, body, related_ids, author, optional idempotency_key)` appends a new open note and returns ID/path/digest. Related IDs must exist. An idempotency key prevents duplicate insertion after a lost response. This is the only mutation callable by coding. Coding cannot update notes, resolve them, confirm proposals, change roadmap, edit knowledge or run migration through Prisma MCP.

## Analysis mutations

Implemented: document/topic updates, proposal create/read/list/confirm, note resolution, source import and legacy inventory/read and migration prepare/list/read/update/apply. Dream/feedback capture, skill updates and Design registration are implemented analysis-only tools.

| Tool | Required input and behavior |
| --- | --- |
| `prisma_document_set` | ID or new-document metadata, body, expected digest for update; validates links and updates derived index |
| `prisma_source_import` | user-provided local path; guarded copy and extraction into sources; original is unchanged |
| `prisma_analysis_set` | topic data and expected digest; consolidation is excluded and needs a proposal |
| `prisma_proposal_create` | kind, target IDs, summary, operations and base digests; validates without applying |
| `prisma_proposal_read` | proposal ID; full persisted operations and current digest for review |
| `prisma_proposals_list` | none; proposed and applied summaries |
| `prisma_proposal_confirm` | proposal ID/digest and explicit confirmation evidence; applies valid operations through a recoverable transaction |
| `prisma_note_resolve` | note ID/digest, outcome (`integrated|dismissed`), reason; integration requires a confirmed applied proposal before deletion |
| `prisma_dream_capture` | title and tentative observation; never creates a roadmap objective automatically |
| `prisma_feedback_record` | title, observed issue and evidence; separate from product knowledge |
| `prisma_skill_set` | scoped Markdown procedure and expected digest for update |
| `prisma_design_record` | existing mockup entry and metadata; registers local HTML/assets without executing code |

Migration is available through the desktop and analysis-only MCP with preview and explicit final confirmation. New-project initialization is implemented as a desktop/core operation and has no MCP tool. Neither is a coding tool. All mutations are root-bound and serialized. Direct external-agent writes remain governed by the role contract, not an operating-system sandbox. Do not claim MCP restrictions protect against arbitrary writes by an agent with filesystem access.

Current change shape: `collection`, `metadata`, `body`, optional `expected_digest`, new-file `slug` and reopen `reason`. Proposal shape: `kind`, `summary`, `operations`, plus `note_id`/`note_digest` for note integration. Confirmation shape: `approved: true`, nonempty `statement` recording explicit user approval. It is an agent attestation, not authenticated proof of a human action. Proposal IDs have a `PROP-<uuid>` prefix. Multi-file updates use a roll-forward journal with a 4 MiB serialized limit; file bodies are limited to 1 MiB and proposals to 50 operations. Artifact reads recover interrupted updates; unexpected external edits stop recovery. Current index output contains grouped titles/links; automatic renames and index summaries remain targets.

## Protocol and errors

Reuse LMBrain Lite's JSON-RPC stdio shape, initialization handshake and schema validation approach. Structured errors: `invalid_input`, `role_denied`, `not_found`, `path_outside_brain`, `unsupported_schema`, `stale_content`, `confirmation_required`, `extraction_failed`, `mutation_incomplete`. Include diagnostics and retryable state, never report success after a failed write. No provider, credential, Git, environment or browser-control tools are part of Prisma.


## Legacy tools (analysis only)

`prisma_legacy_inventory(source)` and `prisma_legacy_read(source, path)` read one explicit `lite|standard` original. `prisma_migration_prepare(source, optional mcp_executable, optional server_key)` stages a draft separately; `prisma_migration_list` and `prisma_migration_read(id)` expose its reviewable content and digest. `prisma_migration_update(id, expected_digest, optional project_summary, operations, remove_ids, review_summary, manual_integration)` revises staged knowledge and roadmap without publishing. `prisma_migration_apply(id, expected_digest, confirmation)` requires the exact reviewed draft and unchanged source/configuration hashes. See contract/migration.md and the product's prisma-mcp/README.md. Coding exposes eight tools; analysis exposes 27.

## Auxiliary areas (analysis only)

`prisma_dream_capture(title, body, optional related_ids)` appends a new `DREAM-<uuid>` artifact with tentative state. `prisma_feedback_record(title, observed_issue, evidence, optional related_ids)` appends a `FEEDBACK-<uuid>` artifact; both issue and evidence are required. Neither changes roadmaps or the knowledge index. Capture retries create separate entries; these tools have no idempotency key.

`prisma_skill_set(path, title, scope, body, optional related_ids, optional expected_digest)` writes only `skills/<slug>.md`. Omit the digest only for a new file. Read the current artifact first for updates, including plain kit procedures identified by `FILE:skills/<slug>.md`. Conversion generates a stable `SKILL-<uuid>` ID; dangling references to the old FILE ID prevent conversion. Procedures are Markdown documentation and are never executed by Prisma.

`prisma_design_record(entry_path, title, knowledge, status, optional update_reason, optional expected_digest)` registers an existing `design/<slug>/index.html` by writing its adjacent manifest. Knowledge must contain existing topic IDs; status is `ready|needs_update`, with a required reason for `needs_update`. Read `prisma_design_list` for the current manifest digest before updating. Custom manifest fields and HTML/assets are preserved. The returned entry digest describes the HTML read during registration; optimistic concurrency covers the manifest. Existing manifests without IDs acquire `DESIGN-<uuid>` identity. Registration does not execute HTML.

Use Refresh in the app after an agent saves auxiliary content. Dream Journal, Kit Feedback and Skills provide discussion entry points into Sessions; Design selects the first newly available preview after Refresh. All four mutations use the existing path guards, lock and recoverable journal.
