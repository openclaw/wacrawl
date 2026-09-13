package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openclaw/wacrawl/internal/store"
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
		_, _ = io.WriteString(stdout, currentVersion()+"\n")
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
	case "sql":
		return a.runSQL(ctx, rest[1:])
	case "web":
		return a.runWeb(ctx, rest[1:])
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
