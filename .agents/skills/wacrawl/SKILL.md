---
name: wacrawl
description: "wacrawl: WhatsApp Desktop archive import/search, slices, backups, repo/release work."
---

# Wacrawl

Use this for WhatsApp Desktop archive questions. `wacrawl` is read-only against WhatsApp data and writes only its own archive.

## Sources

- DB: `~/.wacrawl/wacrawl.db`
- Source: `~/Library/Group Containers/group.net.whatsapp.WhatsApp.shared`
- CLI: `wacrawl`

## Refresh

Check source/archive health:

```bash
wacrawl doctor
```

Import a fresh local snapshot:

```bash
wacrawl import
```

Inspect counts:

```bash
wacrawl status
```

Encrypted Git backup:

```bash
wacrawl backup status
wacrawl backup push
```

## Query Workflow

1. Refresh/import if the question depends on recent WhatsApp state.
2. Resolve chat, sender, date range, media needs, and keyword.
3. Use CLI for chats/messages/search; use JSON for scripts.
4. Report exact date spans, counts, chat names/JIDs, and import freshness.

Common commands:

```bash
wacrawl chats --limit 20
wacrawl messages --after 2026-01-01 --limit 50
wacrawl --json search "query" --from-them
```

## Safety

Do not write into the WhatsApp app container. Do not send messages; this tool is archive/read-only.

## Verification

For repo edits:

```bash
make test
make check
```

Then smoke:

```bash
wacrawl doctor
wacrawl status
```
