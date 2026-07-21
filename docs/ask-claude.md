---
type: Guide
title: Ask Claude
description: The optional in-app Claude panel — what it can do, how it is sandboxed, privacy, and the settings that control it (executable, environment variables, activity log).
tags: [portanote, claude, ai, mcp]
timestamp: 2026-07-20T14:00:00-06:00
---

# Ask Claude

If the [Claude Code CLI](https://claude.com/claude-code) is installed and logged in on the machine running Portanote, a `✳` button appears in the note toolbar. It opens a chat panel that knows which note you have open.

![The Ask Claude panel — a chat drawer beside the editor that summarized the open note and extracted its action items into the To-Do list](screenshot-claude.png)

## What it does

- **Quick actions:** *Summarize*, *Improve* (suggestions only, never silent edits), *Extract tasks* (action items land in your To-Do list, linked back to the note), *Suggest tags*.
- **Free-form:** ask anything about the note or your collection — Claude can search, read, create, and edit notes through Portanote's own MCP tools.
- **Targeted edits:** highlight lines in the editor before sending and Claude receives those line numbers and their content as the region you mean — "fix this", "rewrite these lines", "expand this section" apply right there. A `✂ Targeting lines N–M` chip above the composer shows what's selected.
- **AI tag suggestions:** an opt-in toggle in ⚙ Settings → *Ask Claude* adds a *Generate AI suggestions* button to the tag suggestion row (per note) and an *AI tags* action to the bulk bar (selected notes). Claude reads the note plus your existing tag vocabulary and prefers reusing your tags over inventing new ones. The built-in ✨ suggestions stay instant and offline; Claude only runs when you click.

## How it works, and what it can touch

Each message spawns a fresh headless `claude` process that connects back to this Portanote instance over localhost. It is restricted to Portanote's note/task tools — **no shell, no file access, no web**. Anything it changes goes through the same store as the UI, so edits are indexed instantly and "deleting" is only ever the recoverable trash. Your editor autosaves before each message and locks while Claude works; the note and To-Do list refresh when it finishes.

**Privacy & cost:** messages (and the notes Claude reads to answer them) are sent to Anthropic through your own Claude account, and usage counts against your plan. Each message starts a fresh conversation.

## Settings & troubleshooting (⚙ Settings → Ask Claude)

**Detection.** Portanote auto-detects the `claude` executable and settings file at launch — your `PATH` first, then the usual install locations (`~/.local/bin`, Homebrew, …), so a background-launched instance on macOS still finds it. The settings fields are pre-filled with what was detected; point them at a specific binary or `--settings` file if you'd rather, or clear a field to go back to auto-detect.

**Environment variables.** The **Environment variables** box (one `KEY=VALUE` per line) is merged into every spawned `claude`. Set `NODE_EXTRA_CA_CERTS=/path/to/root.crt` here to make Claude Code trust a corporate TLS-inspecting proxy (Zscaler and the like). Portanote also reads the `env` block from your `claude` settings file and applies it to each turn — including vars Node needs set before startup, which a plain settings file can miss. Where they overlap, the box wins; the section lists what was auto-loaded.

**Activity log.** Below the settings, a log lists recent prompts and any errors. The exact CLI error is captured, so a "not logged in" or certificate problem shows up there.

**The ✳ button doesn't appear?** Open ⚙ Settings → *Ask Claude* to see what was detected — or run `claude` once in a terminal to log in, then restart Portanote.
