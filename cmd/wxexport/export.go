package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/arldka/wxexport/internal/export"
)

func cmdExport(ctx context.Context, g globalFlags, args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	contact := fs.String("contact", "", "contact username, remark, or nickname (required)")
	formats := fs.String("format", "json,csv,sqlite", "comma-separated: json,csv,sqlite")
	outDir := fs.String("out", "", "output directory (default: current directory)")
	from := fs.String("from", "", "inclusive start date YYYY-MM-DD, in --tz")
	to := fs.String("to", "", "inclusive end date YYYY-MM-DD, in --tz")
	kinds := fs.String("kinds", "", "comma-separated kinds to include (default: all)")
	tz := fs.String("tz", "", "IANA timezone for date/iso_week bucketing (default: system local)")
	raw := fs.Bool("raw", false, "include the decompressed raw_content column")
	excelBOM := fs.Bool("excel", false, "add a UTF-8 BOM to CSV files (helps Excel on Windows)")
	force := fs.Bool("force", false, "overwrite existing output files")
	if err := fs.Parse(args); err != nil {
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

	loc := time.Local
	tzName := "Local"
	if want := firstNonEmpty(*tz, e.cfg.TZ); want != "" {
		l, err := time.LoadLocation(want)
		if err != nil {
			return fmt.Errorf("invalid --tz %q: %w", want, err)
		}
		loc, tzName = l, want
	}

	fromT, toT, err := export.DayRange(*from, *to, loc)
	if err != nil {
		return fmt.Errorf("invalid --from/--to date: %w", err)
	}
	var kindSet map[string]bool
	if *kinds != "" {
		kindSet = map[string]bool{}
		for _, k := range strings.Split(*kinds, ",") {
			kindSet[strings.TrimSpace(k)] = true
		}
	}

	wantFormats := map[string]bool{}
	for _, f := range strings.Split(*formats, ",") {
		f = strings.TrimSpace(f)
		if f != "json" && f != "csv" && f != "sqlite" {
			return fmt.Errorf("unknown --format %q (want json, csv, sqlite)", f)
		}
		wantFormats[f] = true
	}

	messages, err := store.Messages(username, *raw)
	if err != nil {
		return err
	}
	rows, counts, unknownDir := export.BuildRows(messages, loc, *raw, export.Filter{From: fromT, To: toT, Kinds: kindSet})

	cs, err := store.Contacts(true)
	if err != nil {
		return err
	}
	displayName := username
	for _, c := range cs {
		if c.Username == username {
			displayName = c.DisplayName()
			break
		}
	}

	dir := firstNonEmpty(*outDir, e.cfg.OutDir, ".")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	base := sanitizeFilename(displayName)
	meta := export.Meta{
		ToolVersion: "0.1.0", ExportedAt: time.Now().UTC(),
		Account: e.account.Name, Contact: export.ContactMeta{Username: username, DisplayName: displayName},
		TZ: tzName, Filters: export.FilterMeta{From: *from, To: *to, Kinds: splitNonEmpty(*kinds)},
		CountsByKind: counts, UnknownDirectionCount: unknownDir,
	}

	var written []string
	if wantFormats["json"] {
		p := filepath.Join(dir, base+".json")
		if err := export.WriteJSON(p, *force, meta, rows); err != nil {
			return err
		}
		written = append(written, p)
	}
	if wantFormats["csv"] {
		mp := filepath.Join(dir, base+"_messages.csv")
		if err := export.WriteMessagesCSV(mp, *force, *raw, *excelBOM, rows); err != nil {
			return err
		}
		written = append(written, mp)
		cp := filepath.Join(dir, base+"_calls.csv")
		if err := export.WriteCallsCSV(cp, *force, *excelBOM, rows); err != nil {
			return err
		}
		written = append(written, cp)
	}
	if wantFormats["sqlite"] {
		p := filepath.Join(dir, base+".sqlite")
		if err := export.WriteSQLite(p, *force, meta, rows); err != nil {
			return err
		}
		written = append(written, p)
	}

	fmt.Printf("exported %d message(s) for %s (%d unknown-direction):\n", len(rows), displayName, unknownDir)
	for _, p := range written {
		fmt.Println("  " + p)
	}
	return nil
}

func splitNonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// sanitizeFilename makes name safe to use as a file base name.
func sanitizeFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == '\x00':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return "export"
	}
	return s
}
