package cli

import (
	"context"
	"errors"
	"flag"

	"github.com/openclaw/wacrawl/internal/store"
	"github.com/openclaw/wacrawl/internal/webui"
)

func (a *app) runWeb(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	port := fs.Int("port", 0, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "web")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("web takes flags only"))
	}
	if *port < 0 || *port > 65535 {
		return usageErr(errors.New("--port must be between 0 and 65535"))
	}
	if a.json {
		return usageErr(errors.New("web does not support --json"))
	}
	return a.withArchiveStore(ctx, func(st *store.Store) error {
		return webui.Serve(ctx, st, webui.Config{Port: *port, Output: a.stdout})
	})
}
