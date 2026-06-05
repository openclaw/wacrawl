package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/steipete/wacrawl/internal/backup"
	"github.com/steipete/wacrawl/internal/store"
	"github.com/steipete/wacrawl/internal/whatsappdb"
)

type cliError struct {
	code int
	err  error
}

func (e *cliError) Error() string { return e.err.Error() }

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ce *cliError
	if errors.As(err, &ce) {
		return ce.code
	}
	return 1
}

type app struct {
	stdout     io.Writer
	stderr     io.Writer
	json       bool
	dbPath     string
	source     string
	syncMode   archiveSyncMode
	syncMaxAge time.Duration
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	global := flag.NewFlagSet("wacrawl", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	jsonOut := global.Bool("json", false, "")
	dbPath := global.String("db", defaultDBPath(), "")
	source := global.String("source", "", "")
	syncFlag := global.String("sync", string(archiveSyncAuto), "")
	syncMaxAge := global.Duration("sync-max-age", 15*time.Minute, "")
	versionFlag := global.Bool("version", false, "")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return nil
		}
		return usageErr(err)
	}
	if *versionFlag {
		_, _ = io.WriteString(stdout, version+"\n")
		return nil
	}
	syncMode, err := parseArchiveSyncMode(*syncFlag)
	if err != nil {
		return usageErr(err)
	}
	a := &app{stdout: stdout, stderr: stderr, json: *jsonOut, dbPath: *dbPath, source: *source, syncMode: syncMode, syncMaxAge: *syncMaxAge}
	rest := global.Args()
	if len(rest) == 0 {
		printUsage(stdout)
		return nil
	}
	if rest[0] == "help" {
		if len(rest) == 1 {
			printUsage(stdout)
			return nil
		}
		if printCommandUsage(stdout, rest[1:]...) {
			return nil
		}
		return usageErr(fmt.Errorf("unknown help topic %q", strings.Join(rest[1:], " ")))
	}
	switch rest[0] {
	case "metadata":
		return a.print(controlManifest())
	case "doctor":
		return a.runDoctor(ctx, rest[1:])
	case "import", "sync":
		return a.runImport(ctx, rest[0], rest[1:])
	case "status":
		return a.runStatus(ctx, rest[1:])
	case "chats":
		return a.runChats(ctx, rest[1:])
	case "contacts":
		return a.runContacts(ctx, rest[1:])
	case "unread":
		return a.runUnread(ctx, rest[1:])
	case "messages":
		return a.runMessages(ctx, rest[1:])
	case "search":
		return a.runSearch(ctx, rest[1:])
	case "backup":
		return a.runBackup(ctx, rest[1:])
	default:
		return usageErr(fmt.Errorf("unknown command %q", rest[0]))
	}
}

func (a *app) withStore(ctx context.Context, fn func(*store.Store) error) error {
	st, err := store.Open(ctx, a.dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func (a *app) withArchiveStore(ctx context.Context, fn func(*store.Store) error) error {
	return a.withStore(ctx, func(st *store.Store) error {
		if err := a.syncArchive(ctx, st); err != nil {
			return err
		}
		return fn(st)
	})
}

func (a *app) runStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "status")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("status takes flags only"))
	}
	return a.withArchiveStore(ctx, func(st *store.Store) error {
		status, err := st.Status(ctx)
		if err != nil {
			return err
		}
		return a.print(status)
	})
}

func (a *app) runDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	source := fs.String("source", a.source, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "doctor")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("doctor takes flags only"))
	}
	src, err := whatsappdb.Discover(ctx, *source)
	if err != nil {
		return err
	}
	report := map[string]any{
		"desktop":  src,
		"db_path":  a.dbPath,
		"readonly": true,
	}
	return a.print(report)
}

func (a *app) runImport(ctx context.Context, command string, args []string) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	source := fs.String("source", a.source, "")
	copyMedia := fs.Bool("copy-media", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, command)
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(fmt.Errorf("%s takes flags only", command))
	}
	return a.withStore(ctx, func(st *store.Store) error {
		stats, err := whatsappdb.ImportWithOptions(ctx, st, whatsappdb.ImportOptions{SourcePath: *source, CopyMedia: *copyMedia})
		if err != nil {
			return err
		}
		return a.print(stats)
	})
}

type contactExport struct {
	Contacts []exportedContact `json:"contacts"`
}

type exportedContact struct {
	DisplayName  string   `json:"display_name"`
	PhoneNumbers []string `json:"phone_numbers"`
}

func (a *app) runContacts(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "export" {
		return usageErr(errors.New("contacts supports export only"))
	}
	fs := flag.NewFlagSet("contacts export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "contacts", "export")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("contacts export takes no arguments"))
	}
	return a.withArchiveStore(ctx, func(st *store.Store) error {
		contacts, err := st.Contacts(ctx)
		if err != nil {
			return err
		}
		return a.print(contactExport{Contacts: exportContacts(contacts)})
	})
}

func exportContacts(contacts []store.Contact) []exportedContact {
	out := make([]exportedContact, 0, len(contacts))
	for _, contact := range contacts {
		name := contactDisplayName(contact)
		phone := strings.TrimSpace(contact.Phone)
		if name == "" || phone == "" {
			continue
		}
		out = append(out, exportedContact{DisplayName: name, PhoneNumbers: []string{phone}})
	}
	return out
}

func contactDisplayName(contact store.Contact) string {
	for _, name := range []string{
		contact.FullName,
		contact.BusinessName,
		strings.TrimSpace(contact.FirstName + " " + contact.LastName),
	} {
		if cleaned := cleanContactName(name, contact); cleaned != "" {
			return cleaned
		}
	}
	return ""
}

func cleanContactName(name string, contact store.Contact) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return ""
	case name == strings.TrimSpace(contact.Phone):
		return ""
	case name == strings.TrimSpace(contact.JID):
		return ""
	case name == strings.TrimSpace(contact.Username):
		return ""
	case name == strings.TrimSpace(contact.LID):
		return ""
	case strings.HasPrefix(name, "@"):
		return ""
	case looksLikePhone(name):
		return ""
	default:
		return name
	}
}

func looksLikePhone(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	digits := 0
	other := 0
	for _, r := range value {
		switch {
		case unicode.IsDigit(r):
			digits++
		case strings.ContainsRune(" +()-.", r):
		default:
			other++
		}
	}
	return digits >= 5 && other == 0
}

func (a *app) runChats(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("chats", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 50, "")
	unread := fs.Bool("unread", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "chats")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("chats takes flags only"))
	}
	return a.withArchiveStore(ctx, func(st *store.Store) error {
		var (
			chats []store.Chat
			err   error
		)
		if *unread {
			chats, err = st.ListUnreadChats(ctx, *limit)
		} else {
			chats, err = st.ListChats(ctx, *limit)
		}
		if err != nil {
			return err
		}
		return a.print(chats)
	})
}

func (a *app) runUnread(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("unread", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 50, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "unread")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("unread takes flags only"))
	}
	return a.withArchiveStore(ctx, func(st *store.Store) error {
		chats, err := st.ListUnreadChats(ctx, *limit)
		if err != nil {
			return err
		}
		return a.print(chats)
	})
}

func (a *app) runMessages(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("messages", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	filter := bindMessageFlags(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "messages")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("messages takes flags only"))
	}
	resolved, err := filter.resolve()
	if err != nil {
		return usageErr(err)
	}
	return a.withArchiveStore(ctx, func(st *store.Store) error {
		msgs, err := st.Messages(ctx, resolved)
		if err != nil {
			return err
		}
		return a.print(msgs)
	})
}

func (a *app) runSearch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	filter := bindMessageFlags(fs)
	if commandWantsHelp(args) {
		printCommandUsage(a.stdout, "search")
		return nil
	}
	flagArgs, query, err := splitSearchArgs(args)
	if err != nil {
		return usageErr(err)
	}
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "search")
			return nil
		}
		return usageErr(err)
	}
	resolved, err := filter.resolve()
	if err != nil {
		return usageErr(err)
	}
	resolved.Query = query
	return a.withArchiveStore(ctx, func(st *store.Store) error {
		msgs, err := st.Search(ctx, resolved)
		if err != nil {
			return err
		}
		return a.print(msgs)
	})
}

func splitSearchArgs(args []string) ([]string, string, error) {
	var flags []string
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if searchFlagNeedsValue(arg) && !strings.Contains(arg, "=") {
				next := i + 1
				if next >= len(args) {
					return nil, "", fmt.Errorf("flag needs an argument: %s", arg)
				}
				flags = append(flags, args[next]) // #nosec G602 -- next is checked against len(args) above.
				i = next
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	if len(positionals) != 1 {
		return nil, "", errors.New("search requires exactly one query")
	}
	return flags, positionals[0], nil
}

func searchFlagNeedsValue(arg string) bool {
	name := strings.TrimPrefix(arg, "-")
	name = strings.TrimPrefix(name, "-")
	if before, _, ok := strings.Cut(name, "="); ok {
		name = before
	}
	switch name {
	case "chat", "sender", "limit", "after", "before":
		return true
	default:
		return false
	}
}

type messageFlags struct {
	chat     *string
	sender   *string
	limit    *int
	after    *string
	before   *string
	fromMe   *bool
	fromThem *bool
	hasMedia *bool
	asc      *bool
}

func bindMessageFlags(fs *flag.FlagSet) messageFlags {
	return messageFlags{
		chat:     fs.String("chat", "", ""),
		sender:   fs.String("sender", "", ""),
		limit:    fs.Int("limit", 50, ""),
		after:    fs.String("after", "", ""),
		before:   fs.String("before", "", ""),
		fromMe:   fs.Bool("from-me", false, ""),
		fromThem: fs.Bool("from-them", false, ""),
		hasMedia: fs.Bool("has-media", false, ""),
		asc:      fs.Bool("asc", false, ""),
	}
}

func (f messageFlags) resolve() (store.MessageFilter, error) {
	if *f.fromMe && *f.fromThem {
		return store.MessageFilter{}, errors.New("--from-me and --from-them are mutually exclusive")
	}
	out := store.MessageFilter{
		ChatJID:  *f.chat,
		Sender:   *f.sender,
		Limit:    *f.limit,
		HasMedia: *f.hasMedia,
		Asc:      *f.asc,
	}
	if *f.fromMe {
		v := true
		out.FromMe = &v
	}
	if *f.fromThem {
		v := false
		out.FromMe = &v
	}
	if strings.TrimSpace(*f.after) != "" {
		t, err := parseTime(*f.after)
		if err != nil {
			return store.MessageFilter{}, err
		}
		out.After = &t
	}
	if strings.TrimSpace(*f.before) != "" {
		t, err := parseTime(*f.before)
		if err != nil {
			return store.MessageFilter{}, err
		}
		out.Before = &t
	}
	return out, nil
}

func (a *app) print(value any) error {
	if a.json {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	}
	switch v := value.(type) {
	case store.ImportStats:
		_, err := fmt.Fprintf(a.stdout, "source=%s\ndb=%s\nchats=%d\ncontacts=%d\ngroups=%d\nparticipants=%d\nmessages=%d\nmedia_messages=%d\nmedia_copied=%d\nmedia_missing=%d\n",
			v.SourcePath, v.DBPath, v.Chats, v.Contacts, v.Groups, v.Participants, v.Messages, v.MediaMessages, v.MediaCopied, v.MediaMissing)
		return err
	case store.Status:
		_, err := fmt.Fprintf(a.stdout, "db=%s\nchats=%d\nunread_chats=%d\nunread_messages=%d\ncontacts=%d\ngroups=%d\nparticipants=%d\nmessages=%d\nmedia_messages=%d\noldest=%s\nnewest=%s\nlast_import=%s\nsource=%s\n",
			v.DBPath, v.Chats, v.UnreadChats, v.UnreadMessages, v.Contacts, v.Groups, v.Participants, v.Messages, v.MediaMessages, formatTime(v.OldestMessage), formatTime(v.NewestMessage), formatTime(v.LastImportAt), v.LastSource)
		return err
	case []store.Chat:
		tw := tabwriter.NewWriter(a.stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "LAST\tKIND\tUNREAD\tMESSAGES\tJID\tNAME")
		for _, c := range v {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\n", formatTime(c.LastMessageAt), c.Kind, c.UnreadCount, c.MessageCount, c.JID, c.Name)
		}
		return tw.Flush()
	case []store.Message:
		for _, m := range v {
			body := firstNonEmpty(m.Snippet, m.Text, m.MediaTitle, m.MessageType)
			if _, err := fmt.Fprintf(a.stdout, "[%s] %s / %s / %s\n%s\n\n", formatTime(m.Timestamp), m.ChatName, firstNonEmpty(m.SenderName, m.SenderJID), m.MessageID, body); err != nil {
				return err
			}
		}
		return nil
	case backup.Result:
		_, err := fmt.Fprintf(a.stdout, "repo=%s\nchanged=%t\nencrypted=%t\nshards=%d\nmessages=%d\n", v.Repo, v.Changed, v.Encrypted, v.Shards, v.Messages)
		return err
	case backup.Manifest:
		_, err := fmt.Fprintf(a.stdout, "encrypted=%t\nshards=%d\nmessages=%d\nexported=%s\n", v.Encrypted, len(v.Shards), v.Counts.Messages, formatTime(v.Exported))
		return err
	default:
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	}
}

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

Examples:
  wacrawl doctor
  wacrawl sync
  wacrawl unread --limit 20
  wacrawl --json --sync never contacts export
  wacrawl --json search "invoice" --from-them --after 2026-01-01
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
  wacrawl %s [--source PATH] [--copy-media]

Flags:
  --source PATH   WhatsApp Desktop source path.
  --copy-media    Copy referenced media files into media/ next to the archive DB.

Examples:
  wacrawl %s
  wacrawl %s --copy-media
  wacrawl --db /tmp/wacrawl.db %s
`, name, name, name, name)
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
	case "backup":
		_, _ = fmt.Fprint(w, `Manage encrypted Git backups of the wacrawl archive.

Usage:
  wacrawl backup <init|push|pull|status> [flags]

Commands:
  init      Create backup config, age identity, and first encrypted backup.
  push      Export the archive and push encrypted shards.
  pull      Restore encrypted shards into the configured archive DB.
  status    Inspect backup config and manifest.

Common flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
  --recipient AGE    Age recipient. Repeatable.
  --no-push          Commit locally without pushing.

Examples:
  wacrawl backup status
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
		_, _ = fmt.Fprint(w, `Export and push encrypted archive shards.

Usage:
  wacrawl backup push [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
  --recipient AGE    Age recipient. Repeatable.
  --no-push          Commit locally without pushing.
`)
	case "backup pull":
		_, _ = fmt.Fprint(w, `Restore encrypted archive shards into the archive database.

Usage:
  wacrawl backup pull [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
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
	default:
		return false
	}
	return true
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "wacrawl.db"
	}
	return filepath.Join(home, ".wacrawl", "wacrawl.db")
}

func usageErr(err error) error {
	return &cliError{code: 2, err: err}
}

func parseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("empty time")
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time %q", value)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
