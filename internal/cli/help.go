package cli

import (
	"fmt"
	"io"
	"strings"
)

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `wacrawl reads local WhatsApp Desktop data into a readonly archive.

Usage:
  wacrawl [--db PATH] [--source PATH] [--json] [--sync auto|always|never] <command> [args]
  wacrawl --version

Commands:
  metadata    Print crawlkit control metadata.
  doctor      Inspect WhatsApp Desktop source and archive paths.
  import      Snapshot WhatsApp Desktop SQLite data into the archive.
  sync        Alias for import.
  status      Show archive status.
  chats       List chats.
  contacts    Export archived contacts.
  unread      List chats with unread messages.
  messages    List archived messages.
  search      Search archived messages.
  sql         Run a read-only SQL query.
  web         Browse the local archive in a private web viewer.
  backup      Init, push, pull, or inspect encrypted Git backups.

Options:
  --db PATH                 Archive database path.
  --source PATH             WhatsApp Desktop source path.
  --sync auto|always|never  Read-time sync policy. Default: auto.
  --sync-max-age DURATION   Staleness window for --sync auto. Default: 15m.
  --json                    Emit JSON output.
  --version                 Print the CLI version.

Import flags:
  --copy-media              Copy referenced media files into the archive media directory.
  --adopt-source            Bind and merge; requires a verified source account identity.
  --restore                 Exactly replace archive rows instead of merging.

Examples:
  wacrawl doctor
  wacrawl sync
  wacrawl unread --limit 20
  wacrawl --json --sync never contacts export
  wacrawl --json search "invoice" --from-them --after 2026-01-01
  wacrawl sql "SELECT count(*) FROM messages"
  wacrawl --sync never web
  wacrawl help messages
`)
}

func commandWantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func printCommandUsage(w io.Writer, topic ...string) bool {
	name := strings.Join(topic, " ")
	switch name {
	case "doctor":
		_, _ = fmt.Fprint(w, `Inspect the WhatsApp Desktop source and archive paths.

Usage:
  wacrawl doctor [--source PATH]

Flags:
  --source PATH   WhatsApp Desktop source path.

Examples:
  wacrawl doctor
  wacrawl --json doctor
`)
	case "import", "sync":
		_, _ = fmt.Fprintf(w, `Snapshot WhatsApp Desktop SQLite data into the archive.

Usage:
  wacrawl %s [--source PATH] [--copy-media] [--adopt-source] [--restore]

Flags:
  --source PATH   WhatsApp Desktop source path.
  --copy-media    Copy referenced media files into media/ next to the archive DB.
  --adopt-source  Bind and merge; requires a verified source account identity.
  --restore       Exactly replace archive rows instead of merging.

Adoption requires account metadata from the Desktop source, even for an empty
archive. A store fingerprint alone is insufficient. If account metadata is
missing, read retained history with --sync never. --restore replaces history
and is not a recovery for missing account identity.

Examples:
  wacrawl %s
  wacrawl %s --copy-media
  wacrawl %s --adopt-source
  wacrawl %s --restore
  wacrawl --db /tmp/wacrawl.db %s
`, name, name, name, name, name, name)
	case "status":
		_, _ = fmt.Fprint(w, `Show archive status, counts, date span, unread counts, and last import metadata.

Usage:
  wacrawl status

Examples:
  wacrawl status
  wacrawl --sync never status
  wacrawl --json status
`)
	case "chats":
		_, _ = fmt.Fprint(w, `List archived chats.

Usage:
  wacrawl chats [--limit N] [--unread]

Flags:
  --limit N   Maximum chats to print. Default: 50.
  --unread    Show only chats with unread messages.

Examples:
  wacrawl chats --limit 20
  wacrawl chats --unread
  wacrawl --json chats --limit 100
`)
	case "contacts", "contacts export":
		_, _ = fmt.Fprint(w, `Export archived contacts.

Usage:
  wacrawl [--json] [--sync auto|always|never] contacts export

Examples:
  wacrawl --json --sync never contacts export
`)
	case "unread":
		_, _ = fmt.Fprint(w, `List chats with unread messages.

Usage:
  wacrawl unread [--limit N]

Flags:
  --limit N   Maximum chats to print. Default: 50.

Examples:
  wacrawl unread
  wacrawl unread --limit 20
`)
	case "messages":
		_, _ = fmt.Fprint(w, `List archived messages.

Usage:
  wacrawl messages [flags]

Flags:
  --chat JID       Filter by chat JID.
  --sender JID     Filter by sender JID.
  --limit N        Maximum messages to print. Default: 50.
  --after TIME     Only messages after RFC3339 or YYYY-MM-DD.
  --before TIME    Only messages before RFC3339 or YYYY-MM-DD.
  --from-me        Only messages sent by me.
  --from-them      Only messages received from others.
  --has-media      Only messages with media metadata.
  --asc            Show oldest messages first.

Examples:
  wacrawl messages --limit 20
  wacrawl messages --chat 1234567890@s.whatsapp.net --asc
  wacrawl messages --after 2026-01-01 --from-them
  wacrawl --json messages --has-media --limit 100
`)
	case "search":
		_, _ = fmt.Fprint(w, `Search archived messages.

Usage:
  wacrawl search [flags] <query>
  wacrawl search <query> [flags]

Flags:
  --chat JID       Filter by chat JID.
  --sender JID     Filter by sender JID.
  --limit N        Maximum messages to print. Default: 50.
  --after TIME     Only messages after RFC3339 or YYYY-MM-DD.
  --before TIME    Only messages before RFC3339 or YYYY-MM-DD.
  --from-me        Only messages sent by me.
  --from-them      Only messages received from others.
  --has-media      Only messages with media metadata.
  --asc            Show oldest messages first.

Examples:
  wacrawl search "invoice"
  wacrawl search "flight" --after 2026-01-01 --from-them
  wacrawl --json search --chat 1234567890@s.whatsapp.net "release notes"
`)
	case "sql":
		_, _ = fmt.Fprint(w, `Run a read-only SQL query against the archive database.

Usage:
  wacrawl sql <select query>

Examples:
  wacrawl sql "SELECT count(*) FROM messages"
  wacrawl --json sql "SELECT chat_jid, count(*) FROM messages GROUP BY chat_jid"
`)
	case "web":
		_, _ = fmt.Fprint(w, `Browse the local archive in a private web viewer.

The viewer binds only to 127.0.0.1 and requires a random key printed in its URL.
It reads archive status, chats, messages, and search results, with inline previews
for local images. Media reads stay confined to known archive and source roots.
The viewer does not expose configuration or write controls.

Usage:
  wacrawl web [--port N]

Flags:
  --port N   Loopback port. Default: choose a free random port.

Examples:
  wacrawl web
  wacrawl --sync never web --port 8787
`)
	case "backup":
		_, _ = fmt.Fprint(w, `Manage encrypted Git backups of the wacrawl archive.

Usage:
  wacrawl backup <init|push|pull|status|snapshots> [flags]

Commands:
  init      Create backup config, age identity, and first encrypted backup.
  push      Export the archive and push encrypted shards.
  pull      Restore encrypted shards into the configured archive DB.
  status    Inspect backup config and manifest.
  snapshots List restorable Git backup snapshots and tags.

Common flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
  --recipient AGE    Age recipient. Repeatable.
  --no-push          Commit locally without pushing.

Examples:
  wacrawl backup status
  wacrawl backup snapshots
  wacrawl backup push
  wacrawl --sync never backup push
  wacrawl backup init --repo ~/Projects/backup-wacrawl --remote https://github.com/steipete/backup-wacrawl.git
`)
	case "backup init":
		_, _ = fmt.Fprint(w, `Initialize encrypted Git backup configuration.

Usage:
  wacrawl backup init [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
  --recipient AGE    Age recipient. Repeatable.
  --no-push          Commit locally without pushing.
`)
	case "backup push":
		_, _ = fmt.Fprint(w, `Export and push encrypted archive shards and copied media.

Usage:
  wacrawl backup push [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
  --recipient AGE    Age recipient. Repeatable.
  --no-push          Commit locally without pushing.
  --no-media         Omit copied media files from this backup.
  --tag NAME         Tag the resulting backup commit.
`)
	case "backup pull":
		_, _ = fmt.Fprint(w, `Restore encrypted archive shards and copied media.

Usage:
  wacrawl backup pull [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
  --no-media         Restore archive rows without copied media files.
  --ref REF          Restore a tag, commit, or branch without changing checkout.
`)
	case "backup status":
		_, _ = fmt.Fprint(w, `Show encrypted backup status and manifest metadata.

Usage:
  wacrawl backup status [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
`)
	case "backup snapshots":
		_, _ = fmt.Fprint(w, `List restorable encrypted backup snapshots from Git history.

Usage:
  wacrawl backup snapshots [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --limit N          Maximum snapshots to list. Default: 20.
`)
	default:
		return false
	}
	return true
}
