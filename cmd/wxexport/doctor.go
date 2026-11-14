package main

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/arldka/wxexport/internal/discover"
	"github.com/arldka/wxexport/internal/keys"
	"github.com/arldka/wxexport/internal/procfs"
)

func cmdDoctor(ctx context.Context, g globalFlags) error {
	fmt.Println("== environment ==")
	in, err := discover.FindInstall(discover.ExecRunner)
	if err != nil {
		fmt.Println("flatpak install : MISSING -", err)
	} else {
		fmt.Printf("flatpak install : %s\nversion         : %s\nbinary          : %s\n", in.Location, in.Version, in.BinaryPath)
	}
	pids := discover.RunningPids(procfs.Default())
	fmt.Printf("running pids    : %v\n", pids)
	fmt.Printf("ptrace_scope    : %d (0 = unrestricted, 1 = descendants only)\n", procfs.PtraceScope())

	e, err := g.load()
	if err != nil {
		fmt.Println("account         : MISSING -", err)
		return nil
	}
	fmt.Printf("\n== account ==\nfolder          : %s\nown wxid        : %s\ndb_storage      : %s\n", e.account.Name, e.account.Wxid, e.account.DBStorage)

	c, inv, err := loadKeys(e)
	if err != nil {
		return err
	}
	fmt.Printf("keys file       : %s (passphrase cached: %v)\n", e.keysPath, c.Passphrase() != nil)
	_, res, rerr := keys.Resolve(c, inv)
	counts := map[keys.Status]int{}
	for _, r := range res {
		counts[r.Status]++
	}
	var names []string
	for s := range counts {
		names = append(names, string(s))
	}
	sort.Strings(names)
	fmt.Printf("databases       : %d total\n", len(res))
	for _, n := range names {
		fmt.Printf("  %-12s %d\n", n, counts[keys.Status(n)])
	}
	if rerr != nil {
		fmt.Println("key status      : NO VALID KEY -", rerr)
	}
	for _, r := range res {
		if r.Status == keys.StatusError {
			fmt.Fprintf(os.Stderr, "  %s: %v\n", r.DB.Rel, r.Err)
		}
	}
	return nil
}
