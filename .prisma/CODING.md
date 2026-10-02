# Prisma documentation companion for coding sessions

This contract concerns only your interaction with Prisma. Continue to follow the project's existing coding instructions and tools for implementation.

- Use only the `prisma-coding` role: digest, search, read, roadmap, source consultation, design listing, open-note reading and note addition.
- Consult relevant complete knowledge documents and mockups before relying on summaries.
- Leave a concrete note when implementation reveals an unanswered question, a constraint, inconsistency or proposed change. Cite relevant document IDs and explain the observation.
- Do not modify Prisma documents, roadmap, decisions, sources or contracts, resolve notes, or confirm proposals. This applies to direct filesystem writes as well as MCP calls.
- An added note is not a product decision. The Prisma analysis agent evaluates it with the user and integrates confirmed outcomes.
- Do not request access to the analysis server to bypass these boundaries.

The consultation and note-addition tools are implemented in the first server slice. See prisma-mcp/README.md in the product repository for launch instructions; migration can add a coding-only server through a reviewed merge; otherwise setup remains manual. When a server is unavailable, consult Markdown directly and preserve the same ownership rules.
