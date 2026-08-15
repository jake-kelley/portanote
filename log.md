# Change log

Release history, newest first. Full diffs live in the [GitHub releases](https://github.com/jake-kelley/portanote/releases).

- **2026-08-15 — v1.6.9**: the Ask Claude panel says when it resumed a folder's conversation instead of claiming a fresh start every time you switch folders; clearing a thread and then changing folders no longer wipes the wrong folder's conversation.
- **2026-08-14 — v1.6.8**: Ask Claude remembers the conversation. Context now carries from message to message instead of every question starting over, scoped to the open note's folder — notes in the same folder share a thread, a note in another folder gets its own, and a folder's conversation resumes when you return to it.
- **2026-07-20 — v1.6.7**: folder rename in the sidebar (pencil button; double-click still works) and a rename_folder MCP tool that renames or moves whole subtrees.
- **2026-07-15 — v1.6.6**: frontmatter Portanote doesn't own is preserved on save; block-form (Obsidian-style) tags are read instead of emptied.
- **2026-07-15 — v1.6.5**: configurable update repository — pull releases from your own fork or a self-managed GitLab instance.
- **2026-07-15 — v1.6.4**: opt-in AI tag suggestions via the claude CLI; documentation restructured as an OKF bundle.
- **2026-07-09 — v1.6.3**: new Portanote logo; refreshed screenshots.
- **2026-07-08 — v1.6.2**: auto-load env from claude settings.json; refreshed screenshots.
- **2026-07-08 — v1.6.1**: pass configurable env vars to the spawned claude.
- **2026-07-08 — v1.6.0**: Ask Claude settings + activity log.
- **2026-07-08 — v1.5.1**: find the claude CLI beyond PATH.
- **2026-07-08 — v1.5.0**: Ask Claude panel.
- **2026-07-08 — v1.4.1**: version bump to exercise the in-app updater.
- **2026-07-08 — v1.4.0**: in-app self-update from GitHub Releases.
- **2026-07-08 — v1.3.0**: folder-scoped search with a global toggle.
- **2026-07-08 — v1.2.1**: refreshed screenshots; docs for sync; footer spacing fix.
- **2026-07-08 — v1.2.0**: sync-with-disk button and rescan API.
- **2026-07-08 — v1.1.0**: folders are real directories; MCP server.
- **2026-07-05 — v1.0.1**: README autorun-on-startup instructions (Windows/macOS).
- **2026-07-05 — v1.0.0**: first full release with complete README and release workflow.
