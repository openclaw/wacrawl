# 🧾 wacrawl

![wacrawl banner](docs/assets/readme-banner.jpg)

WhatsApp archaeology with encrypted receipts.

Read-only local archive and search for the macOS WhatsApp Desktop app.

`wacrawl` copies WhatsApp Desktop's local SQLite databases into a temporary
snapshot, imports the useful chat data into its own SQLite archive, and gives
you scriptable commands for status, chat listing, message listing, and full-text
search.

It is for local inspection. It does not send messages, decrypt backups, talk to
WhatsApp Web, or write back into WhatsApp's app container.

## Install

Homebrew is the easiest path. Install directly from the OpenClaw tap:

```bash
brew install openclaw/tap/wacrawl
```

After that, upgrades stay simple:

```bash
brew update
brew upgrade openclaw/tap/wacrawl
```

Or from source:

```bash
go install github.com/openclaw/wacrawl/cmd/wacrawl@latest
```

Check the installed binary:

```bash
wacrawl --version
```

## Quick Start

First, check whether `wacrawl` can see the local WhatsApp Desktop data:

```bash
wacrawl doctor
```

Sync a fresh local archive:

```bash
wacrawl sync
wacrawl import --copy-media
```

Inspect what was imported. Read commands sync automatically by default, so
`status`, `chats`, `messages`, and `search` refresh the archive before reading
when the local WhatsApp Desktop source is newer:

```bash
wacrawl status
wacrawl chats --limit 20
wacrawl unread --limit 20
wacrawl messages --limit 20
```

Search message text:

```bash
wacrawl search "release notes"
```

Use JSON for scripts:

```bash
wacrawl --json search "invoice" --from-them --after 2026-01-01
```

## What It Reads

On macOS, WhatsApp Desktop stores app data in:

```text
~/Library/Group Containers/group.net.whatsapp.WhatsApp.shared
```

`wacrawl` currently imports from:

```text
ChatStorage.sqlite
ContactsV2.sqlite
Message/Media/
```

It writes its own archive to:

```text
~/.wacrawl/wacrawl.db
```

Override either path when needed:

```bash
wacrawl --source "$HOME/Library/Group Containers/group.net.whatsapp.WhatsApp.shared" doctor
wacrawl --db /tmp/wacrawl.db import
```

## Safety

- Opens WhatsApp data read-only.
- Copies SQLite database, WAL, and SHM files into a temp snapshot before import.
- Replaces only the `wacrawl` archive database.
- Does not modify WhatsApp databases, settings, contacts, chats, or media.
- Does not use the WhatsApp network protocol.
- Does not upload data during normal archive/search commands. `backup push`
  uploads only age-encrypted backup shards when you explicitly run it.

The archive can contain private message data. Keep `~/.wacrawl/wacrawl.db`
local and out of commits, backups, and shared logs unless that is intentional.

## Commands

### `doctor`

Inspect the source path and database shape:

```bash
wacrawl doctor
wacrawl --json doctor
```

Reports source availability, discovered database files, row counts, message date
range, and importer schema notes.

### `import`

Snapshot WhatsApp Desktop data and replace the local archive in one transaction:

```bash
wacrawl import
wacrawl import --copy-media
```

`sync` is the same command with a clearer name:

```bash
wacrawl sync
wacrawl sync --copy-media
```

Imports:

- chats
- contacts
- groups
- group participants
- messages
- media metadata and local media paths

By default, media paths continue to point at WhatsApp Desktop's app container.
Pass `--copy-media` to copy referenced media files into `media/` next to the
archive database and rewrite copied message media paths to that archive copy.
Missing media files are counted in the import output and do not fail the import.

### `status`

Show archive counts and import metadata:

```bash
wacrawl status
```

Includes chat, unread-chat, unread-message, contact, group, participant,
message, media-message, oldest, newest, last-import, and source fields.

By default, `status` first syncs the archive when the last sync is older than
`--sync-max-age` and the WhatsApp Desktop source has newer data.

### `chats`

List chats ordered by newest message:

```bash
wacrawl chats
wacrawl chats --limit 100
wacrawl chats --unread
```

Unread state comes from WhatsApp Desktop's per-chat unread counter. Message
rows do not expose a reliable incoming per-message "read by me" flag.

### `unread`

List only chats with unread messages:

```bash
wacrawl unread
wacrawl unread --limit 100
```

### `messages`

List archived messages:

```bash
wacrawl messages
wacrawl messages --chat 1234567890@s.whatsapp.net
wacrawl messages --after 2026-01-01 --from-them
wacrawl messages --has-media --json
```

Filters:

```text
--chat JID       Restrict to one chat.
--sender JID     Restrict to one sender.
--limit N        Max rows. Default: 50.
--after DATE     RFC3339 timestamp or YYYY-MM-DD.
--before DATE    RFC3339 timestamp or YYYY-MM-DD.
--from-me        Only outgoing messages.
--from-them      Only incoming messages.
--has-media      Only messages with media metadata.
--asc            Oldest first.
```

### `search`

Search the archive with SQLite FTS5:

```bash
wacrawl search "launch"
wacrawl search "invoice" --from-them --after 2026-01-01
wacrawl --json search "restaurant"
```

Search uses message text, chat name, sender name, and media title fields. It
accepts the same filters as `messages`.

### `sql`

Run a read-only SQL query against the archive database:

```bash
wacrawl sql "SELECT count(*) FROM messages"
wacrawl sql "SELECT name FROM sqlite_master WHERE type='table'"
wacrawl --json sql "SELECT chat_jid, count(*) FROM messages GROUP BY chat_jid ORDER BY 2 DESC LIMIT 10"
```

Only single read-only `SELECT` statements are accepted. Use `--db PATH` to query
an alternate archive database.

## Sync Behavior

`wacrawl` keeps normal reads fresh without a daemon or background service.
Before `status`, `chats`, `messages`, `search`, or `sql`, it checks the archive's
last import time. If the archive is stale, it inspects the WhatsApp Desktop
source and imports a fresh snapshot only when the source is ahead.

The default policy is:

```text
--sync auto
--sync-max-age 15m
```

Sync modes:

```text
--sync auto     Sync before reads when the archive is stale and source is ahead.
--sync always   Force a sync before every read command.
--sync never    Read only the existing archive.
```

Examples:

```bash
wacrawl search "release notes"
wacrawl --sync always status
wacrawl --sync never --json messages --limit 10
wacrawl --sync-max-age 1h chats
```

If the WhatsApp Desktop source is unavailable and the archive already has data,
`--sync auto` warns on stderr and continues with the existing archive.
`--sync always` treats an unavailable source as an error.

## Encrypted Git Backup

`wacrawl` can back up the archive to a Git repository using age-encrypted JSONL
shards. This is meant for a private repository such as
`https://github.com/steipete/backup-wacrawl`, but the message data is encrypted
before Git sees it.

The backup repo contains:

```text
README.md
manifest.json
data/chats.jsonl.gz.age
data/contacts.jsonl.gz.age
data/groups.jsonl.gz.age
data/group_participants.jsonl.gz.age
data/messages/YYYY/MM.jsonl.gz.age
```

`manifest.json` is intentionally cleartext so a machine can inspect backup
freshness, public age recipients, counts, shard paths, encrypted byte sizes, and
plaintext hashes without decrypting message contents. It does not contain
message text, chat names, contacts, participant IDs, or media metadata. Those
fields live inside the `*.jsonl.gz.age` shards.

### Command Cheat Sheet

Use these most of the time:

```bash
# First-time setup on a machine.
wacrawl backup init \
  --repo ~/Projects/backup-wacrawl \
  --remote https://github.com/steipete/backup-wacrawl.git

# Refresh WhatsApp data if needed, encrypt, commit, and push to GitHub.
wacrawl backup push

# Pull the Git backup, decrypt, verify, and import into the local archive.
wacrawl backup pull

# List restorable commits and any snapshot tags.
wacrawl backup snapshots

# Inspect the backup manifest without decrypting message data.
wacrawl backup status
```

Useful safety variants:

```bash
# Force a fresh WhatsApp import before writing the backup.
wacrawl --sync always backup push

# Write and commit locally, but do not push to GitHub.
wacrawl backup push --no-push

# Create a named checkpoint while pushing a backup.
wacrawl backup push --tag snapshot/before-phone-migration

# Restore into a throwaway database for testing.
wacrawl --db /tmp/wacrawl-restore-test.db backup pull
wacrawl --db /tmp/wacrawl-restore-test.db --sync never status

# Restore a historical tag, commit, or branch without changing the backup checkout.
wacrawl --db /tmp/wacrawl-history.db backup pull --ref snapshot/before-phone-migration
```

You should not need to run `git` manually for normal use. `backup push` handles
the backup repo fetch/rebase, commit, and push. A normal `backup pull` syncs the
current archive branch; `backup pull --ref` fetches refs and reads the requested
Git objects without switching or rewriting the backup checkout.

Every changed backup is already a Git commit. Use `--tag NAME` to add an
optional named checkpoint without moving an existing tag. Tag names and commit
metadata are visible to anyone who can inspect the Git repository, so keep tag
names non-sensitive. `backup snapshots` lists the newest manifest-changing
commits and their tags; use `--limit N` to control the history depth.

### Encryption and Security Model

Backups use the Go `filippo.io/age` library with X25519 age identities. There
is no backup password. Each machine has an age identity file, usually:

```text
~/.wacrawl/age.key
```

That file contains an `AGE-SECRET-KEY-...` private identity and is written with
0600 permissions. Its matching public recipient starts with `age1...` and is
safe to place in `~/.wacrawl/backup.json`, `manifest.json`, or docs.

For each shard, `wacrawl backup push`:

1. Exports rows from the local archive as deterministic JSONL.
2. Gzip-compresses the JSONL with a fixed gzip timestamp.
3. Encrypts the compressed bytes with age for every configured recipient.
4. Writes only the encrypted `*.jsonl.gz.age` shard to Git.
5. Writes `manifest.json` with cleartext metadata used for status, diffing, and restore verification.

`wacrawl backup pull` does the reverse: it pulls/rebases the backup repo,
checks manifest shard paths, decrypts each shard with the local age identity,
verifies the shard hash, validates cross-table references, and imports the
snapshot into the configured archive database in one transaction.

What the backup protects:

- A GitHub read-only compromise or accidental clone does not reveal message text,
  contacts, chat names, participant IDs, or media metadata.
- Each encrypted shard can be decrypted by any listed age recipient, so multiple
  machines can share one backup without sharing one private key.
- Age provides encrypted-file integrity; corrupted or wrong-key shards fail to
  decrypt, and `wacrawl` also checks manifest hashes after decrypting.

What remains visible in Git:

- `manifest.json` is cleartext.
- The manifest reveals export time, public recipients, table names, row counts,
  shard paths, encrypted byte sizes, and plaintext shard hashes.
- Message shard paths reveal activity by year and month, for example
  `data/messages/2026/04.jsonl.gz.age`.
- Git history reveals backup cadence and which encrypted shards changed.

Important limits:

- This is not end-to-end provenance. Someone who can push to the backup repo can
  replace the backup with different data encrypted to your public recipient.
  Use normal GitHub access control and review unexpected backup commits.
- If `~/.wacrawl/age.key` is lost and no other configured recipient exists, the
  encrypted backup cannot be restored.
- If an age identity is compromised, remove its public recipient, run
  `wacrawl backup push` to re-encrypt current shards, and consider rewriting or
  deleting old Git history because older commits may still be decryptable with
  the compromised key.
- X25519 age recipients are not post-quantum. They are a practical modern
  default, but not a post-quantum archival guarantee.
- The local archive database `~/.wacrawl/wacrawl.db` and the WhatsApp Desktop
  source data remain plaintext on the machine. Protect the machine and local
  backups accordingly.

### Initial Setup

Initialize the backup repository and local age identity:

```bash
wacrawl backup init \
  --repo ~/Projects/backup-wacrawl \
  --remote https://github.com/steipete/backup-wacrawl.git
```

This writes `~/.wacrawl/backup.json`, creates `~/.wacrawl/age.key` if needed,
clones or initializes the local backup checkout, and prints the public age
recipient.

The generated config looks like this:

```json
{
  "repo": "~/Projects/backup-wacrawl",
  "remote": "https://github.com/steipete/backup-wacrawl.git",
  "identity": "~/.wacrawl/age.key",
  "recipients": ["age1..."]
}
```

Keep `~/.wacrawl/age.key` private. The public `age1...` recipient can be stored
in `backup.json`; the `AGE-SECRET-KEY-...` identity must stay local or in a
password manager.

### Push

Push an encrypted backup:

```bash
wacrawl backup push
```

`backup push` first pulls/rebases the configured backup checkout, then uses the
normal read-time sync policy. With the default `--sync auto --sync-max-age 15m`,
it refreshes the local archive only when the WhatsApp Desktop source is stale
and newer than the archive. Then it exports stable JSONL, gzip-compresses each
shard, encrypts each shard for every configured recipient, updates
`manifest.json`, removes stale encrypted shards, commits, and pushes the backup
repo.

Pass `--tag NAME` to tag the resulting snapshot commit. If the archive is
unchanged, the tag points at the existing current snapshot. Existing tags are
never moved to a different commit.

Re-running `backup push` without archive changes leaves Git clean. The command
prints the repo path, whether anything changed, whether the backup is encrypted,
the shard count, and the message count.

Use `--no-push` for local dry runs that commit into the backup checkout but do
not push to the remote:

```bash
wacrawl backup push --no-push
```

### Restore

Restore from the backup repo:

```bash
wacrawl backup pull
```

`backup pull` pulls/rebases the configured backup repo, decrypts every shard with
the local age identity, verifies each plaintext shard hash from the manifest,
validates cross-table references, and replaces the configured `wacrawl` archive
database in one import transaction.

Restore a historical tag, commit, or branch with `--ref`:

```bash
wacrawl --db /tmp/wacrawl-history.db backup pull --ref snapshot/before-phone-migration
```

Historical restore resolves the ref to a commit and reads its manifest and
encrypted shards directly from Git objects. It does not checkout that commit or
change the backup repository's current branch.

To test a restore without touching your real archive:

```bash
wacrawl --db /tmp/wacrawl-restore-test.db backup pull
wacrawl --db /tmp/wacrawl-restore-test.db --sync never status
```

### Status

Inspect backup metadata:

```bash
wacrawl backup status
```

This reports encryption status, shard count, message count, export timestamp,
and repo path. It reads `manifest.json`; it does not need to decrypt shards.

### Multiple Machines

Each machine that should restore needs its own age identity. On the new machine:

```bash
wacrawl backup init \
  --repo ~/Projects/backup-wacrawl \
  --remote https://github.com/steipete/backup-wacrawl.git
```

Copy the printed public recipient (`age1...`) into the `recipients` list in
`~/.wacrawl/backup.json` on a machine that can already decrypt the backup, then
run:

```bash
wacrawl backup push
```

After that push, newly written shards are encrypted for all configured
recipients. If you added a recipient after data already existed, run a normal
`wacrawl backup push`; unchanged plaintext shards are re-encrypted when the
manifest/config changes.

For personal setup, storing a copy of `~/.wacrawl/age.key` in 1Password is a
good recovery path. Do not commit the identity file. Do not paste the
`AGE-SECRET-KEY-...` value into issues, logs, docs, or chat.

### Flags

Useful flags:

```text
--config PATH        Backup config path. Default: ~/.wacrawl/backup.json
--repo PATH          Local backup Git checkout.
--remote URL         Backup Git remote.
--identity PATH      Local age identity. Default: ~/.wacrawl/age.key
--recipient AGE      Public age recipient. Repeat for multiple machines.
--no-push            Commit locally but do not push.
```

### Recovery Checklist

On a new Mac:

```bash
brew install openclaw/tap/wacrawl
git clone https://github.com/steipete/backup-wacrawl.git ~/Projects/backup-wacrawl
mkdir -p ~/.wacrawl
```

Then restore `~/.wacrawl/age.key` from your password manager and create
`~/.wacrawl/backup.json` pointing at the clone:

```json
{
  "repo": "~/Projects/backup-wacrawl",
  "remote": "https://github.com/steipete/backup-wacrawl.git",
  "identity": "~/.wacrawl/age.key",
  "recipients": ["age1..."]
}
```

Finally:

```bash
wacrawl backup pull
wacrawl --sync never status
```

If decryption fails, the local `identity` does not match any recipient used for
the encrypted shards. If Git push fails, fix normal GitHub permissions for the
backup repository; the archive data is already encrypted before the push.

## Global Flags

```text
--db PATH               Archive database path. Default: ~/.wacrawl/wacrawl.db
--source PATH           WhatsApp Desktop source path.
--sync MODE             Read-time sync policy: auto, always, or never. Default: auto.
--sync-max-age DURATION Staleness window for --sync auto. Default: 15m.
--json                  Emit JSON instead of human-readable output.
--version               Print the CLI version.
```

Import/sync flags:

```text
--copy-media            Copy referenced media during import/sync.
```

## Data Format Notes

WhatsApp Desktop uses CoreData-style SQLite tables. The importer currently knows
about:

```text
ZWACHATSESSION
ZWAMESSAGE
ZWAMEDIAITEM
ZWAGROUPINFO
ZWAGROUPMEMBER
```

Important details:

- WhatsApp timestamps are seconds since `2001-01-01T00:00:00Z`.
- `ZWAMESSAGE.Z_PK` is used as the source row identity.
- `ZSTANZAID` is not unique enough for archive identity.
- Group senders are resolved through `ZWAMESSAGE.ZGROUPMEMBER`.
- Media is joined through both `ZWAMESSAGE.ZMEDIAITEM` and
  `ZWAMEDIAITEM.ZMESSAGE`.
- WhatsApp's own search database uses a custom `wa_tokenizer`; `wacrawl` builds
  a portable FTS5 index instead.

## Development

Requires Go 1.26 or newer.

```bash
make check
```

Runs:

```bash
golangci-lint run ./...
./scripts/coverage.sh 85.0
go build -o bin/wacrawl ./cmd/wacrawl
```

Extra release-parity checks:

```bash
go test -count=1 -race ./...
goreleaser release --snapshot --clean --skip=publish
```

Coverage must stay at or above 85%.

## Release

Releases are tag-driven through GoReleaser.

```bash
git tag -a v0.2.0 -m "Release 0.2.0"
git push origin main --tags
```

CI publishes GitHub release artifacts for:

```text
darwin/amd64
darwin/arm64
linux/amd64
linux/arm64
windows/amd64
windows/arm64
```

The Homebrew formula lives in:

```text
~/Projects/openclaw-homebrew-tap/Formula/wacrawl.rb
```

## License

MIT. See `LICENSE`.
