# Archive identities and data model

WhatsApp Desktop stores CoreData-style records in SQLite. `wacrawl` imports the useful entities into a portable archive with an FTS5 search index.

## Source tables

The importer reads these WhatsApp tables:

```text
ZWACHATSESSION
ZWAMESSAGE
ZWAMEDIAITEM
ZWAGROUPINFO
ZWAGROUPMEMBER
Axolotl.sqlite: ZWAZMDACCOUNT (account identity only)
```

## Identity and merge rules

- WhatsApp timestamps are seconds since `2001-01-01T00:00:00Z`.
- `ZWAMESSAGE.Z_PK` is retained as `messages.source_row_pk`. Ordinary rows use the same value for the unique archive `source_pk` and map to `messages.event_id` as `wa:<source_pk>`.
- When WhatsApp reuses an archived message row for a reaction, the original keeps its identity and the reaction receives a deterministic JSON-safe high-range `source_pk` plus a `wa-reaction:<source_row_pk>:<digest>` event ID. The digest covers the chat, reaction stanza, and reaction target, so repeat imports deduplicate without discarding either event; the raw reused row remains available in `source_row_pk` as provenance.
- Routine merges bind the archive to the canonical source path, a hashed CoreData store fingerprint, and a separately hashed account JID. Event overlap is not an account-identity substitute.
- Legacy archives without verified account binding require one explicit `--adopt-source`. Use a separate `--db` for another account or `--restore` for intentional source replacement.
- `ZSTANZAID` is not unique enough to identify archived messages.
- Canonical entities carry `deleted_at`, `deletion_source`, `deletion_reason`, and `last_seen_at`; an unobserved row is never implicitly tombstoned.
- Prior observable message payloads are append-only `message_revisions` rows keyed by stable event ID.
- Group senders resolve through `ZWAMESSAGE.ZGROUPMEMBER`.
- Media joins through both `ZWAMESSAGE.ZMEDIAITEM` and `ZWAMEDIAITEM.ZMESSAGE`.
- WhatsApp's search database uses its own `wa_tokenizer`; `wacrawl` builds a portable SQLite FTS5 index instead.

## Imported entities

The archive contains contacts, chats, groups, group participants, messages, message revisions, media metadata and local media paths, source identities, and import metadata. Normal readers exclude tombstoned entities; encrypted backups retain them so exact restore does not discard source deletion history.

See the [command reference](commands.md#import-and-sync) for merge, adoption, restore, and media-copy behavior.
