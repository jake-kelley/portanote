---
type: Design Note
title: Design notes
description: Rationale for Portanote design decisions — currently why search is a lexical index rather than a vector database.
tags: [portanote, design, search]
timestamp: 2026-07-20T14:00:00-06:00
---

# Design notes

## Search: why a lexical index and not a vector DB

Semantic search would need an embedding model (~100 MB plus a native runtime per platform), which fights the single-binary, no-install promise — and for note lookup a well-tuned lexical index is usually *better*: exact terms, prefixes as you type, title/tag boosting, and zero latency. If semantic search ever becomes worth it, the API already returns scored results, so a local embedding sidecar can merge in without changing the UI.
