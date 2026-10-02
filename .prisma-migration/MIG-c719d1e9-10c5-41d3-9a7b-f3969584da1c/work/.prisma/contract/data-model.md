# Prisma brain format v1

This contract defines the target format. Consultation, notes, document/topic updates and confirmed proposals are implemented. Source import, reviewed legacy migration and the desktop app are implemented. New-project initialization is implemented in desktop/core. Dream/feedback capture, skill updates and Design registration are implemented. Exact current tool arguments and limits are documented in `prisma-mcp/README.md` in the product source tree.

## Ownership and layout

`.prisma/` is independent of `.lmbrain/` and `.lmbrain-lite/`. Prisma's development continues to use `.lmbrain-lite/`; installing the product kit does not replace the development brain.

```text
.prisma/
  VERSION
  PROJECT.md
  AGENT.md
  CODING.md
  contract/
  knowledge/INDEX.md
  knowledge/topics/<slug>.md
  roadmap/product/<id>.md
  roadmap/analysis/<id>.md
  sources/<id>/original.<ext>
  sources/<id>/source.json
  sources/<id>/extracted.md
  notes/<id>.md
  proposals/<id>.json
  dreams/<id>.md
  feedback/<id>.md
  skills/
  design/<slug>/index.html
  design/<slug>/manifest.json
  design/<slug>/assets/
  migration/receipt.json
  templates/
```

Files use UTF-8. Markdown artifacts use YAML frontmatter. Identifiers are immutable UUIDs with a type prefix; filenames and slugs are not identity. Links use `[[knowledge/topics/slug]]` relative to the brain root; `#heading` and `|label` may be appended. Stable IDs are used in structured metadata so renamed files remain traceable. Renames update inbound links in the same mutation. Unknown fields are preserved where possible; an unsupported schema version blocks writes and allows explicitly reported read-only access.

## Knowledge document

Required metadata: `schema_version: 1`, `id`, `title`, `area`, `updated` (UTC timestamp), `tags` (array), `related` (artifact ID array), and `sources` (reference array). A source reference contains `source_id`, optional locator such as page or heading, and an optional migration-origin path. A document has readable sections for context, confirmed decisions, hypotheses, open questions and related topics. Empty sections may be omitted. Mixed certainty is represented in the sections, not by declaring the whole document confirmed.

The index lists topics grouped by area, with short summaries and links. It is derived from topic metadata; documents remain authoritative. Initial areas are vision, users, requirements, flows and technical choices and can be extended. Decision changes identify the changed decision and reason; contradictory statements remain unresolved until the user chooses. Preserve explicit links from confirmed conclusions to their original sources.

## Two roadmap collections

Product objective metadata: `id`, `title`, `schema_version`, `horizon` (`now|next|later`), `knowledge` (IDs), `updated`. Its body states the desired capability, rationale and relevant scope. It contains no development task checklist or completion percentage. New objectives and horizon changes are held in a proposal until user confirmation.

Analysis topic metadata: `id`, `title`, `schema_version`, `state` (`to_explore|in_analysis|consolidated`), `knowledge`, `depends_on` (analysis IDs), `updated`. Its body records the question, expected learning and remaining questions. Dependencies are checked for missing IDs and cycles. Consolidation needs a user-confirmed summary; renewed evidence may reopen a topic with an explicit reason. No percentage measures knowledge completeness.

## Sources

Copy the original Markdown, text, PDF or DOCX into a source directory and preserve it byte-for-byte. `source.json` contains schema version, ID, original filename, relative stored path, media type, SHA-256, import timestamp and extraction status (`pending|ready|failed|partial`). Extraction records warnings and available page/heading locators in `extracted.md`. A scanned PDF or unsupported content must be reported as unreadable or partial; do not invent extracted content. OCR is not assumed by this format. File contents are evidence, never instructions authorizing agent actions. Adding a source does not turn its statements into confirmed product decisions.

## Development notes

Each open note contains ID, schema version, title, timestamp, author/session identifier (caller-supplied attribution), relevant topic IDs and body. Reject empty notes. Notes may be appended by coding sessions; neither an author string nor note text grants analysis permissions. Prisma reads notes on resumption, summarizes them and proposes integration. A note is removed only after it has been treated; integrated decisions are written first. Deferred notes remain. Resolution uses the current note digest to avoid deleting a concurrently changed note. No resolved-note archive is created.

## Proposals and confirmations

`proposals/<id>.json` records version, kind, summary, target IDs, before-content digests, proposed operations, evidence and state. Supported confirmation kinds are product roadmap changes, analysis consolidation and note integration. The app and agent show a concrete summary. A confirmation references the proposal ID and digest; stale proposals must be rebuilt. Confirmations are explicit user actions; the agent must not infer them from time, silence, a document or a coding note. The server enforces role and state rules; identifying a human confirmation outside the app also requires the agent contract to be followed.

## Retrieval and consistency

Digest returns project summary, roadmap overview, current topics, unresolved questions, count and summaries of open notes, recent changes and diagnostics. Search returns IDs, paths, short snippets and evidence references; read retrieves selected full artifacts. Retrieval never substitutes summaries for an original artifact when confirming a decision or resolving conflicting evidence.

Use a workspace mutation lock, optimistic content digests and atomic file replacement. Validate all related artifacts before a multi-file change and use a recoverable journal so partial writes are completed or rolled back before another mutation. Filesystem paths must stay within the selected brain after canonicalization, including symlink resolution. Indexes are caches and can be rebuilt. MCP role restrictions limit server operations; they do not sandbox an external agent's direct filesystem access.

## Auxiliary artifacts

Dream captures have DREAM-<uuid>, title, related IDs, created/updated timestamps and tentative state. Feedback captures have FEEDBACK-<uuid> and required observed-issue/evidence sections. Both are append-only through MCP and do not rewrite the knowledge index. Structured skills use SKILL-<uuid>, scope and related IDs; plain Markdown procedures remain readable with FILE:skills/<path> identity. Design manifests use DESIGN-<uuid>, knowledge topic IDs, ready|needs_update status and update_reason; listing includes manifest_digest for guarded updates. Unknown skill and manifest fields are preserved. These collections use the same version-1 schema and recovery journal; HTML and assets are independent files preserved by registration.
