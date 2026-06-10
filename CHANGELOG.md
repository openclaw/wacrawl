# Changelog

## [0.2.7] - Unreleased

### Fixed

- Clamp invalid WhatsApp timestamp sentinels so JSON reads survive existing archives and fresh imports (#16, thanks @rmorgans).

## [0.2.6] - 2026-06-07

### Added

- Add `metadata --json` crawlkit control metadata for schedulers and local automation.

### Changed

- Move source, install, and release automation references to `openclaw/wacrawl` and `openclaw/tap`.
- Update Go to 1.26.4 and refresh Go dependencies, including `crawlkit` to v0.11.0 and `modernc.org/sqlite` to v1.52.0.

### Fixed

- Resolve WhatsApp Desktop `Media/...` paths through `Message/Media` before copying archive media (#9, thanks @pasogott).

## [0.2.5] - 2026-05-15

### Changed

- Move stable archive-store SQLite reads and writes to sqlc-generated wrappers while keeping runtime schema setup, dynamic message/search filters, and WhatsApp Desktop source readers handwritten.

### Fixed

- Improve WhatsApp Desktop group sender-name resolution with profile push names while preserving readable message-level push-name fallbacks (#7, thanks @michalparkola).

## [0.2.4] - 2026-05-08

### Fixed

- Update the baked-in CLI version fallback so binaries installed with `go install` report the released version.

## [0.2.3] - 2026-05-08

### Fixed

- Format the backup coverage regression test so the release branch passes CI
  lint.

## [0.2.2] - 2026-05-08

### Fixed

- Keep the backup regression suite above the CI coverage floor after moving
  shared age encryption helpers into `crawlkit`.

## [0.2.1] - 2026-05-08

### Changed

- Reuse `crawlkit`'s shared encrypted backup helpers for age identities,
  JSONL/Gzip shard encryption, hashes, and restore verification.

### Added

- Add command-specific help menus with examples for `doctor`, `import`, `sync`, `status`, `chats`, `unread`, `messages`, `search`, and backup subcommands.
- Add `import --copy-media` / `sync --copy-media` to copy referenced WhatsApp media into the archive media directory while treating missing files as non-fatal import stats.
- Surface WhatsApp Desktop per-chat unread counts in `status` and `chats`, with `chats --unread` and an `unread` shortcut command.

### Fixed

- Merge duplicate WhatsApp Desktop chat, group, and group participant rows during import so older app data does not fail archive sync on unique constraints.

### Security

- Document the encrypted Git backup threat model, visible metadata, key recovery, and rotation limits.
- Reject backup manifest shard paths that do not point to encrypted files under the backup `data/` tree.

## [0.2.0] - 2026-04-27

### Added

- Add encrypted Git backups with `backup init`, `backup push`, `backup pull`, and `backup status`, storing WhatsApp archive data as age-encrypted JSONL gzip shards in a Git repository.
- Add multi-machine backup support with explicit age recipients, recipient-aware manifests, and automatic re-encryption of unchanged shards when recipients change.
- Add restore verification for encrypted backups, including plaintext shard hashes, cross-table validation, and import into a configured archive database.
- Add read-time sync for `status`, `chats`, `messages`, and `search`, with `--sync auto|always|never`, `--sync-max-age`, and `sync` as an alias for `import`.
- Add a `wacrawl` Codex skill for local WhatsApp archive workflows.

### Changed

- Expand the README with Homebrew install instructions, automatic sync behavior, encrypted Git backup setup, command cheat sheet, multi-machine setup, and recovery checklist.
- Document the `backup-wacrawl` repository layout and restore flow in the generated backup README.

### Fixed

- Allow `search` filters before or after the query, so documented examples like `wacrawl search "invoice" --from-them` work as expected.
- Keep Go module metadata tidy and CI-clean after adding age encryption dependencies.

## [0.1.0] - 2026-04-25

### Added

- Initial read-only WhatsApp Desktop archive CLI.
- macOS source discovery for
  `~/Library/Group Containers/group.net.whatsapp.WhatsApp.shared`.
- Safe SQLite snapshot import for `ChatStorage.sqlite` and `ContactsV2.sqlite`.
- Archive schema for chats, contacts, groups, group participants, messages, and
  FTS5 search.
- Commands: `doctor`, `import`, `status`, `chats`, `messages`, and `search`.
- JSON output mode for scripting.
- Message filters for chat, sender, date range, direction, media presence, sort
  order, and limit.
- WhatsApp CoreData extraction for `ZWACHATSESSION`, `ZWAMESSAGE`,
  `ZWAMEDIAITEM`, `ZWAGROUPINFO`, and `ZWAGROUPMEMBER`.
- Apple-epoch timestamp conversion.
- Group sender resolution through `ZWAMESSAGE.ZGROUPMEMBER`.
- Media metadata extraction through both message-to-media join paths.
- Build, lint, coverage, and test automation through `make check`.
- GitHub Actions CI mirroring Discrawl: lint, tests, race tests, dependency
  checks, vulnerability scan, secret scan, and GoReleaser snapshot check.
- GoReleaser config for macOS, Linux, and Windows release archives.
- Release workflow for `v*` tags and manual tag reruns.
- `--version` flag with release-time ldflags injection.

### Changed

- Project now targets Go 1.26.
- Dependencies updated, including `modernc.org/sqlite` v1.50.0.
- Linting tightened with `golangci-lint` v2 configuration.

### Security

- Import is read-only against WhatsApp's app container.
- WhatsApp SQLite files are copied to a temporary snapshot before extraction.
- Archive writes are isolated to the configured `wacrawl` database.

### Quality

- Coverage gate added at 85% total statement coverage.
- Current test coverage: 86.3%.
- Focused tests cover CLI behavior, archive storage, import fixtures, filtering,
  search, JSON output, schema edge cases, and failure paths.
