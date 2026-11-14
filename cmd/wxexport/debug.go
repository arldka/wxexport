package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/arldka/wxexport/internal/wcdb"
)

func cmdDebug(ctx context.Context, g globalFlags, args []string) error {
	if len(args) == 0 || args[0] != "types" {
		return errors.New("usage: wxexport debug types --contact NAME")
	}
	fs := flag.NewFlagSet("debug types", flag.ContinueOnError)
	contact := fs.String("contact", "", "contact username, remark, or nickname (required)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *contact == "" {
		return errors.New("--contact is required")
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

	username, err := resolveContact(store, *contact)
	if err != nil {
		return err
	}

	hist, err := store.TypeHistogram(username)
	if err != nil {
		return err
	}
	fmt.Printf("type/sub histogram for %s (%d distinct):\n\n", username, len(hist))
	for _, h := range hist {
		fmt.Printf("type=%-6d sub=%-6d count=%-6d sample: %s\n", h.Type, h.Sub, h.Count, truncateLine(h.Sample, 150))
	}
	return nil
}

// resolveContact accepts a username, remark, or nickname and returns the matching username. It
// errors clearly on no match or an ambiguous (multiple) match.
func resolveContact(store *wcdb.Store, query string) (string, error) {
	cs, err := store.Contacts(true)
	if err != nil {
		return "", err
	}
	q := strings.ToLower(query)
	var matches []wcdb.ContactSummary
	for _, c := range cs {
		if c.Username == query {
			return c.Username, nil // exact username match wins immediately
		}
		if strings.Contains(strings.ToLower(c.DisplayName()), q) {
			matches = append(matches, c)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no contact matching %q", query)
	case 1:
		return matches[0].Username, nil
	default:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = fmt.Sprintf("%s (%s)", m.DisplayName(), m.Username)
		}
		return "", fmt.Errorf("%q matches multiple contacts, be more specific: %s", query, strings.Join(names, ", "))
	}
}
