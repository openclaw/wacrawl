package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/openclaw/wacrawl/internal/store"
)

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
