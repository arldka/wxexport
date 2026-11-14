package main

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/arldka/wxexport/internal/discover"
	"github.com/arldka/wxexport/internal/keys"
	"github.com/arldka/wxexport/internal/procfs"
	"github.com/arldka/wxexport/internal/tui"
)

func runTUI(ctx context.Context, g globalFlags) int {
	e, err := g.load()
	if err != nil {
		fmt.Println("error:", err)
		return 1
	}

	status := tui.EnvStatus{Account: e.account.Wxid}
	if in, err := discover.FindInstall(discover.ExecRunner); err == nil {
		status.InstallVersion = in.Version
	}
	status.Running = len(discover.RunningPids(procfs.Default())) > 0

	c, inv, err := loadKeys(e)
	if err == nil {
		core := discover.CoreDBs(inv)
		if ks, _, rerr := keys.Resolve(c, core); rerr == nil || len(ks) > 0 {
			status.DBTotal, status.DBKeyed = len(core), len(ks)
			status.KeyPresent = len(ks) > 0
		}
	}

	// Only attempt to snapshot/decrypt if a key actually validated; otherwise the home screen
	// still launches and reports the missing key (via status) instead of crashing.
	var backend tui.Backend
	if status.KeyPresent {
		store, cleanup, err := openStore(ctx, e)
		if err != nil {
			fmt.Println("error:", err)
			return 1
		}
		defer cleanup()
		status.SnapshotAt = time.Now()
		backend = tui.NewBackend(store, status)
	} else {
		backend = tui.NewBackend(nil, status)
	}
	p := tea.NewProgram(tui.New(backend))
	if _, err := p.Run(); err != nil {
		fmt.Println("error:", err)
		return 1
	}
	return 0
}
