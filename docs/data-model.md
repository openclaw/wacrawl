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
- Archive `source_pk` and `event_id` remain stable across Desktop relogins. Existing archive event IDs and revision links survive migration. Desktop `ZWAMESSAGE.Z_PK` is a source-local row number, not a cross-login identity.
- Source mappings retain the account, Desktop store, raw row and event identity associated with each canonical event. Multiple login stores can point to the same event. Reused rows can describe distinct events, including reactions; these retain separate identities and provenance.
- Imports match overlapping events conservatively across verified same-account stores. Unambiguous contact-provided JID/LID links establish equivalent chat and sender identifiers; names and message overlap never establish person identity. Incomplete or ambiguous matches remain distinct. An unchanged import adds no new events or revisions merely because Desktop row IDs changed.
- Routine merges bind the archive to the canonical source path and a separately hashed account JID. A replacement Desktop store is accepted only for the same verified account. Event overlap is not an account-identity substitute. A relogin uses ordinary `import` against the existing archive, never exact `--restore`.
- Legacy archives without verified account binding require one explicit `--adopt-source`. Use a separate `--db` for another account or `--restore` for intentional source replacement.
- `ZSTANZAID` is not unique enough to identify archived messages.
- Canonical entities carry `deleted_at`, `deletion_source`, `deletion_reason`, and `last_seen_at`; an unobserved row is never implicitly tombstoned.
- Prior observable message payloads are append-only `message_revisions` rows keyed by stable event ID. Source mappings and distinct raw source payloads are retained in `message_sources` and `source_observations`; encrypted backups preserve both. Recorded timestamps describe when the importer observed a payload, not an inferred upstream edit time.
- Group senders resolve through `ZWAMESSAGE.ZGROUPMEMBER`.
- Media joins through both `ZWAMESSAGE.ZMEDIAITEM` and `ZWAMEDIAITEM.ZMESSAGE`.
- WhatsApp's search database uses its own `wa_tokenizer`; `wacrawl` builds a portable SQLite FTS5 index instead.

## Imported entities

The archive contains contacts, chats, groups, group participants, messages, message revisions, media metadata and local media paths, source identities, and import metadata. Normal readers exclude tombstoned entities; encrypted backups retain them so exact restore does not discard source deletion history.

See the [command reference](commands.md#import-and-sync) for merge, adoption, restore, and media-copy behavior.
