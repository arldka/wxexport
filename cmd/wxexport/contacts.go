package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/arldka/wxexport/internal/wcdb"
)

func cmdContacts(ctx context.Context, g globalFlags, args []string) error {
	fs := flag.NewFlagSet("contacts", flag.ContinueOnError)
	search := fs.String("search", "", "filter by display name/username substring (case-insensitive)")
	showAll := fs.Bool("all", false, "also show groups and official accounts")
	asJSON := fs.Bool("json", false, "output as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	e, err := g.load()
	if err != nil {
		return err
	}
	store, cleanup, err := openStore(ctx, e)
	if err != nil {
		return err
	}
	defer cleanup()

	cs, err := store.Contacts(*showAll)
	if err != nil {
		return err
	}
	if *search != "" {
		q := strings.ToLower(*search)
		var filtered []wcdb.ContactSummary
		for _, c := range cs {
			if strings.Contains(strings.ToLower(c.DisplayName()), q) || strings.Contains(strings.ToLower(c.Username), q) {
				filtered = append(filtered, c)
			}
		}
		cs = filtered
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(cs)
	}
	for _, c := range cs {
		last := "-"
		if c.LastTimestamp > 0 {
			last = time.Unix(c.LastTimestamp, 0).Format("2006-01-02")
		}
		fmt.Printf("%-25s %-22s %s  %s\n", c.DisplayName(), c.Username, last, truncateLine(c.Summary, 40))
	}
	fmt.Printf("\n%d contact(s)\n", len(cs))
	return nil
}

func truncateLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
