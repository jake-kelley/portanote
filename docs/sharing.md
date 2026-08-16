---
type: Guide
title: Sharing & importing notes
description: Sending a note to another Portanote user as a share code or a .portanote bundle, and importing bundles, codes, foreign Markdown, or a whole dropped folder with its images.
tags: [portanote, sharing, import, markdown, obsidian]
timestamp: 2026-08-16T16:00:00-06:00
---

# Sharing and importing notes

Send a note to someone else running Portanote, and bring notes in from anywhere
else — both through one button each.

## Sharing

Open a note and press **Share**. Portanote picks how to send it based on what
the note carries:

- **No images, or small ones** — you get a one-line code beginning
  `portanote1:`, already on your clipboard. Paste it into a chat message, an
  email, wherever. Any images under 256 KB in total travel inside it.
- **Larger images** — you get a `.portanote` file instead. On Windows it is
  placed on your clipboard as a file, so pressing Ctrl+V in Teams, Slack or
  Explorer attaches it the same way copying from a folder would. There is a
  Download button either way.

Why two forms: base64 makes data about a third larger, and PNG and JPEG are
already compressed, so a single 500 KB screenshot would become roughly 667 KB
of text — past what most chat clients will accept in one message. Text is the
nicer thing to send when it fits, and a file is the honest thing to send when
it doesn't.

If the clipboard copy fails, Portanote says so rather than claiming success.
On macOS the file-clipboard path is best effort; on Linux it isn't supported
and you get the download.

## Importing

Press **Import** in the sidebar. One drop zone takes four things:

- a `.portanote` file someone sent you
- a `portanote1:` code, pasted into the box
- one or more `.md` files — Portanote's own, or from Obsidian, Hugo, Jekyll,
  or nothing in particular
- a whole folder of Markdown, with its images

You get a preview first — titles, tags, image counts, and any warnings — then
you choose the destination folder and confirm.

The folder you choose is the **root**. If what you're importing has structure,
the shape below the common prefix is kept: a bundle holding
`Work/Runbooks/a` and `Work/Contacts/b` dropped into `Inbox` becomes
`Inbox/Runbooks/a` and `Inbox/Contacts/b`, not two notes flattened together.

### What happens to an imported note

- **It gets a new local id.** The sender's id is never reused, so importing the
  same note twice gives you two notes rather than a collision.
- **Its dates are kept.** A Hugo `date:` or an Obsidian file's modification
  time becomes the created date, so an old note doesn't arrive pretending to be
  new.
- **Frontmatter Portanote doesn't own survives.** Aliases, `cssclass`,
  `draft:`, `categories:` — all preserved word for word, including the `date:`
  key itself, so the file still works in the tool it came from.
- **Images are re-filed.** They're copied into `attachments/` under fresh
  names and the references in the body are rewritten to match. A reference that
  can't be resolved is reported in the preview, and the note still imports.
- **Nothing is overwritten.** A note whose title matches one you already have is
  flagged in the preview and then imported alongside it.

`[[Wiki links]]` resolve by title, so a bundle of notes that link to each other
keeps working after import. The flip side is that an imported `[[Deployment
checklist]]` will point at *your* note of that name if you have one.

## What doesn't travel

Only images are carried. Everything else in a note is text and comes through
as-is.

## Safety

A bundle is a file from someone else's machine, so Portanote treats it as
untrusted: names inside the archive are never used as paths (so an entry called
`../../evil.exe` has nowhere to go), image contents are checked against their
actual bytes rather than their extension, and there are limits on entry size,
total size and entry count so a crafted archive can't exhaust memory or disk.

Bundles are not encrypted or signed. Anyone who has the file or the code can
read the note, and Portanote can't tell you who really sent it — treat a share
exactly as you'd treat the message it arrived in.
