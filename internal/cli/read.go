package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/openclaw/wacrawl/internal/store"
	"github.com/openclaw/wacrawl/internal/whatsappdb"
)

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
	restore := fs.Bool("restore", false, "")
	adoptSource := fs.Bool("adopt-source", false, "")
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
	if *restore && *adoptSource {
		return usageErr(errors.New("--restore and --adopt-source are mutually exclusive"))
	}
	return a.withStore(ctx, func(st *store.Store) error {
		stats, err := whatsappdb.ImportWithOptions(ctx, st, whatsappdb.ImportOptions{SourcePath: *source, CopyMedia: *copyMedia, Restore: *restore, AdoptSource: *adoptSource})
		if err != nil {
			return err
		}
		return a.print(stats)
	})
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
