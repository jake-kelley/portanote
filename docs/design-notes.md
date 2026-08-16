---
type: Design Note
title: Design notes
description: Rationale for Portanote design decisions — why search is a lexical index rather than a vector database, and why a share is one archive format sent two ways.
tags: [portanote, design, search, sharing]
timestamp: 2026-08-16T16:00:00-06:00
---

# Design notes

## Search: why a lexical index and not a vector DB

Semantic search would need an embedding model (~100 MB plus a native runtime per platform), which fights the single-binary, no-install promise — and for note lookup a well-tuned lexical index is usually *better*: exact terms, prefixes as you type, title/tag boosting, and zero latency. If semantic search ever becomes worth it, the API already returns scored results, so a local embedding sidecar can merge in without changing the UI.

## Sharing: one format, sent two ways

A share is always the same thing — a zip holding `manifest.json`, the notes as
Markdown, and their images. What changes is how it travels: under 256 KB it is
base64'd into a `portanote1:` code you can paste into a chat, and above that it
stays a `.portanote` file. The alternative, a JSON envelope for codes and a zip
for files, would have meant two writers, two readers, and two sets of bugs; the
importer instead has exactly one path, and the transport is a detail it decides
before parsing anything.

Text is the nicer thing to send when it fits, and there is a hard reason it
often doesn't: base64 inflates by a third, and PNG and JPEG are already
compressed, so a single 500 KB screenshot becomes ~667 KB of text — past what
most chat clients accept in one message.

Putting the file on the clipboard has to happen in Go, not the browser. The
async Clipboard API only writes `text/plain`, `text/html` and `image/png`;
`application/zip` throws, and a custom `web application/zip` format is
invisible to a native app like Teams. So the server stages the bundle in the
temp directory and hands the OS a real file reference — `CF_HDROP` on Windows,
via `syscall.NewLazyDLL` rather than a dependency. Temp rather than a folder in
the notes directory, because a `shared/` folder would have to become a reserved
name in a tree whose whole promise is that it is just your files.

Imported notes always get a fresh local id. Reusing the sender's would make
importing the same note twice a collision rather than two notes, and ids are
the anchor for wiki-links and task links.
