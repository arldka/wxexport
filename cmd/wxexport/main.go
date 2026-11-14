// Command wxexport exports WeChat 4.x (flatpak) 1:1 chat and call history.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/arldka/wxexport/internal/config"
	"github.com/arldka/wxexport/internal/discover"
	"github.com/arldka/wxexport/internal/keys"
)

const usage = `wxexport - export WeChat 4.x (flatpak) 1:1 chat and call history

Proof of concept, use at your own risk. Use only on your own account and data.

Usage:
  wxexport doctor
  wxexport key capture|set|check|forget
  wxexport contacts [--search S] [--json]
  wxexport debug types --contact NAME
  wxexport export --contact NAME --format json,csv,sqlite --out DIR
                   [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--kinds k1,k2]
                   [--tz ZONE] [--raw] [--excel] [--force]
  wxexport [global flags] <command>

Global flags: --data-dir DIR  --account NAME  --config FILE
`

// env is everything resolved from flags, config and the filesystem.
type env struct {
	cfg      config.Config
	cfgPath  string
	keysPath string
	account  discover.Account
	home     string
}

type globalFlags struct{ dataDir, account, config string }

func (g *globalFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&g.dataDir, "data-dir", "", "xwechat_files directory (default: auto-detect)")
	fs.StringVar(&g.account, "account", "", "account folder name (default: most recently active)")
	fs.StringVar(&g.config, "config", "", "config file (default: $XDG_CONFIG_HOME/wxexport/config.toml)")
}

func (g globalFlags) load() (*env, error) {
	e := &env{}
	var err error
	if e.home, err = os.UserHomeDir(); err != nil {
		return nil, err
	}
	e.cfgPath = g.config
	if e.cfgPath == "" {
		if e.cfgPath, err = config.ConfigPath(); err != nil {
			return nil, err
		}
	}
	if e.cfg, err = config.Load(e.cfgPath); err != nil {
		return nil, fmt.Errorf("config %s: %w", e.cfgPath, err)
	}
	if e.keysPath, err = config.KeysPath(); err != nil {
		return nil, err
	}
	root := firstNonEmpty(g.dataDir, e.cfg.DataDir)
	var roots []string
	if root != "" {
		roots = []string{root}
	} else {
		roots = discover.DataRoots(e.home)
	}
	if len(roots) == 0 {
		return nil, errors.New("no xwechat_files directory found; use --data-dir")
	}
	want := firstNonEmpty(g.account, e.cfg.Account)
	for _, r := range roots {
		accs, err := discover.Accounts(r)
		if err != nil {
			continue
		}
		for _, a := range accs {
			if want == "" || a.Name == want || a.Wxid == want {
				e.account = a
				return e, nil
			}
		}
	}
	return nil, errors.New("no matching WeChat account found")
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("wxexport", flag.ContinueOnError)
	var g globalFlags
	g.register(fs)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return runTUI(ctx, g)
	}
	var err error
	switch rest[0] {
	case "doctor":
		err = cmdDoctor(ctx, g)
	case "key":
		err = cmdKey(ctx, g, rest[1:])
	case "contacts":
		err = cmdContacts(ctx, g, rest[1:])
	case "debug":
		err = cmdDebug(ctx, g, rest[1:])
	case "export":
		err = cmdExport(ctx, g, rest[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n%s", rest[0], usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

// loadKeys loads the cache and validates it against the core databases.
func loadKeys(e *env) (*keys.Cache, []keys.DB, error) {
	inv, err := discover.Inventory(e.account)
	if err != nil {
		return nil, nil, err
	}
	c, err := keys.Load(e.keysPath)
	if err != nil {
		return nil, nil, err
	}
	if c == nil {
		c = &keys.Cache{Account: e.account.Name}
	}
	return c, inv, nil
}
