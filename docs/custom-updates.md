---
type: Guide
title: Updating from your own repository
description: Pointing the in-app updater at a fork or a self-managed GitLab instance, the tokens for private repos, and the release assets your repository must publish.
tags: [portanote, updates, github, gitlab, self-hosted]
timestamp: 2026-07-20T14:00:00-06:00
---

# Updating from your own repository

By default the in-app updater pulls releases from [jake-kelley/portanote](https://github.com/jake-kelley/portanote). If you run your own fork or an internal mirror — a self-managed GitLab instance, say — point **⚙ Settings → Updates → Update repository** at it:

```
https://gitlab.example.com/infra/portanote
```

Leave the field empty to use the default. The rest of Settings behaves the same; the check reports which host it reached.

- **GitHub** (`github.com/owner/repo`) uses the GitHub releases API. Private repos read a token from `PORTANOTE_GITHUB_TOKEN` or `GITHUB_TOKEN`.
- **Any other host** is treated as a **GitLab** instance and read through its `/api/v4` releases API (nested groups are fine). Private projects read a token from `PORTANOTE_GITLAB_TOKEN` or `GITLAB_TOKEN`, sent as `PRIVATE-TOKEN`.

Tokens live in the environment, never in your settings file.

## What your releases must contain

The same asset names this project publishes:

- `portanote-windows-amd64.exe`
- `portanote-macos-arm64`
- `sha256sums.txt` listing their digests

`scripts/build.ps1` produces the binaries. On GitLab, attach them as release asset links using exactly those names. The updater verifies every download against `sha256sums.txt` and refuses a release that doesn't ship one, wherever it came from.
