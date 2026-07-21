---
type: Guide
title: MCP server
description: Portanote's built-in Model Context Protocol endpoint — connecting Claude Code, Claude Desktop, and stdio-only clients, the tool list, and the safety model.
tags: [portanote, mcp, ai, claude]
timestamp: 2026-07-20T14:00:00-06:00
---

# MCP server

While Portanote is running it also serves a [Model Context Protocol](https://modelcontextprotocol.io) endpoint at `http://127.0.0.1:8737/mcp` (Streamable HTTP transport, implemented in the same dependency-free binary). Any MCP client can connect and work with your notes — search, read, create, edit, organize, and manage the to-do list.

## Connect a client

**Claude Code** (one-time, from any directory):

```sh
claude mcp add --transport http portanote http://127.0.0.1:8737/mcp
```

The repo also ships a `.mcp.json`, so Claude Code sessions started inside the project folder pick the server up automatically.

**Claude Desktop:** Settings → Connectors → *Add custom connector* → URL `http://127.0.0.1:8737/mcp`.

**Clients that only speak stdio** can bridge with [`mcp-remote`](https://www.npmjs.com/package/mcp-remote): command `npx`, args `mcp-remote http://127.0.0.1:8737/mcp`.

> If port 8737 was busy, Portanote walks upward to the next free port — the startup log prints the actual MCP URL. Use `-port` to pin it.

## Tools

| Tool | What it does |
|------|--------------|
| `search_notes` | Full-text search (title, tags, body) with relevance scores |
| `list_notes` | List notes, filterable by folder (incl. subfolders), tag, starred |
| `read_note` | Full Markdown body + backlinks for one note |
| `create_note` | New note with optional body, folder, tags |
| `update_note` | Edit title/body/folder/tags/starred; `trashed: true` moves to trash |
| `list_folders` / `list_tags` | Browse your organization scheme |
| `rename_folder` | Rename a folder, or move a whole subtree under a new parent |
| `rescan_notes` | Re-index the notes folder after files changed outside Portanote |
| `list_tasks` / `add_task` / `update_task` | Work the standalone to-do list |

## Safety

The MCP surface can *trash* notes (recoverable in the UI) but deliberately has no permanent-delete tool. The endpoint follows the same bind rules as the UI (localhost-only by default; `-host subnet` extends it to your subnet), rejects browser cross-origin requests to block DNS-rebinding, and — like the rest of Portanote — has no authentication, so only expose it on networks you trust.
