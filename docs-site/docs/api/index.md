---
id: index
title: API reference
---

The public HTTP API of GitStack is described by a single OpenAPI 3 contract
(`api/openapi.yaml` in the repository). The reference is generated from that
file at build time with `@redocly/cli build-docs`; nothing is written by hand.

**[Open the API reference](pathname:///api-reference/index.html)**

The same contract produces the Go and TypeScript clients and the server
interfaces, so the reference, the clients and the servers cannot drift apart.
