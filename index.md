# Portanote documentation

Index of the documentation in this repository (an [OKF](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md) knowledge bundle).

- [README](/README.md) — what Portanote is, how to run it on Windows and macOS, everyday use, and your data on disk.
- [docs/autostart.md](/docs/autostart.md) — the one-command install scripts (download + verify, deploy to Documents, start at login) and the manual launcher steps they automate.
- [docs/ask-claude.md](/docs/ask-claude.md) — the optional Ask Claude panel: capabilities, sandboxing, privacy, settings, troubleshooting.
- [docs/mcp.md](/docs/mcp.md) — the built-in MCP server: connecting clients, the tool list, the safety model.
- [docs/custom-updates.md](/docs/custom-updates.md) — pointing the in-app updater at your own fork or a self-managed GitLab instance.
- [docs/design-notes.md](/docs/design-notes.md) — rationale for design decisions (lexical search vs a vector DB).
- [archive/color-highlight.md](/archive/color-highlight.md) — archived implementation of the inline highlight/color editor feature, kept for possible restoration.
- [.claude/skills/verify/SKILL.md](/.claude/skills/verify/SKILL.md) — the Claude Code skill for building and driving Portanote end-to-end when verifying changes.
- [log.md](/log.md) — release history, newest first.

The `notes/` directory (when present) holds a live notes folder used during development, not documentation; `docs/` holds the guides above plus the README's images.
