# Design Mockups

Operator-loaded HTML mockups that support milestones. They are plain files, not managed artifacts.

## Package convention

```text
design/
  checkout-flow/
    index.html
    README.md        optional context
    manifest.json    optional {"title": "...", "description": "..."}
    assets/
```

Self-contained single `.html` files directly in this folder are also supported.

Reference a mockup from a milestone's Notes section so the agent finds it.
