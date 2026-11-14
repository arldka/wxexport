// Package tui implements the Bubble Tea v2 interface described in docs/SPEC.md §U.
//
// Scope note: relative to the original §U sketch, this implementation covers four screens
// (home, contacts, conversation, export) using a simple custom filterable list instead of
// bubbles/list, and does not include a dedicated interactive key-capture screen — the existing
// `wxexport key capture` CLI command already covers that fully; the home screen just reports key
// status and points at it. This is a deliberate scope reduction, not an oversight.
package tui

import (
	"fmt"
	"time"

	"github.com/arldka/wxexport/internal/export"
	"github.com/arldka/wxexport/internal/wcdb"
)

// EnvStatus is the environment/account summary shown on the home screen.
type EnvStatus struct {
	InstallVersion string
	Account        string
	Running        bool
	KeyPresent     bool
	DBTotal        int
	DBKeyed        int
	SnapshotAt     time.Time
}

// ExportRequest configures one export run, driven from the export screen.
type ExportRequest struct {
	Username string
	Formats  []string // "json", "csv", "sqlite"
	OutDir   string
	Raw      bool
	TZ       string // "" = local
}

// ExportSummary is what the export screen shows after a run.
type ExportSummary struct {
	Files []string
	Count int
}

// Backend is everything the TUI needs from the rest of the program. It exists so the TUI can be
// tested against a fake without a real WeChat install or decrypted databases.
type Backend interface {
	Status() (EnvStatus, error)
	Contacts(showAll bool) ([]wcdb.ContactSummary, error)
	Messages(username string) ([]wcdb.Message, error)
	Export(req ExportRequest) (ExportSummary, error)
}

// wcdbBackend is the real Backend, backed by an open wcdb.Store.
type wcdbBackend struct {
	store  *wcdb.Store
	status EnvStatus
}

// NewBackend wraps an already-open store and a precomputed status (status is a snapshot taken
// once at startup; the "refresh" action on the home screen re-takes it via the caller's own
// reopen logic, which lives in cmd/wxexport since it owns the snapshot/key lifecycle).
func NewBackend(store *wcdb.Store, status EnvStatus) Backend {
	return &wcdbBackend{store: store, status: status}
}

var errNoStore = fmt.Errorf("no key captured yet; run `wxexport key capture`")

func (b *wcdbBackend) Status() (EnvStatus, error) { return b.status, nil }

func (b *wcdbBackend) Contacts(showAll bool) ([]wcdb.ContactSummary, error) {
	if b.store == nil {
		return nil, errNoStore
	}
	return b.store.Contacts(showAll)
}

func (b *wcdbBackend) Messages(username string) ([]wcdb.Message, error) {
	if b.store == nil {
		return nil, errNoStore
	}
	return b.store.Messages(username, false)
}

func (b *wcdbBackend) Export(req ExportRequest) (ExportSummary, error) {
	if b.store == nil {
		return ExportSummary{}, errNoStore
	}
	loc := time.Local
	tzName := "Local"
	if req.TZ != "" {
		l, err := time.LoadLocation(req.TZ)
		if err != nil {
			return ExportSummary{}, fmt.Errorf("invalid tz %q: %w", req.TZ, err)
		}
		loc, tzName = l, req.TZ
	}
	messages, err := b.store.Messages(req.Username, req.Raw)
	if err != nil {
		return ExportSummary{}, err
	}
	rows, counts, unknownDir := export.BuildRows(messages, loc, req.Raw, export.Filter{})

	cs, err := b.store.Contacts(true)
	if err != nil {
		return ExportSummary{}, err
	}
	displayName := req.Username
	for _, c := range cs {
		if c.Username == req.Username {
			displayName = c.DisplayName()
			break
		}
	}

	meta := export.Meta{
		ToolVersion: "0.1.0", ExportedAt: time.Now().UTC(),
		Contact: export.ContactMeta{Username: req.Username, DisplayName: displayName},
		TZ:      tzName, CountsByKind: counts, UnknownDirectionCount: unknownDir,
	}

	var out ExportSummary
	for _, f := range req.Formats {
		switch f {
		case "json":
			p := req.OutDir + "/" + displayName + ".json"
			if err := export.WriteJSON(p, true, meta, rows); err != nil {
				return out, err
			}
			out.Files = append(out.Files, p)
		case "csv":
			mp := req.OutDir + "/" + displayName + "_messages.csv"
			if err := export.WriteMessagesCSV(mp, true, req.Raw, false, rows); err != nil {
				return out, err
			}
			out.Files = append(out.Files, mp)
			cp := req.OutDir + "/" + displayName + "_calls.csv"
			if err := export.WriteCallsCSV(cp, true, false, rows); err != nil {
				return out, err
			}
			out.Files = append(out.Files, cp)
		case "sqlite":
			p := req.OutDir + "/" + displayName + ".sqlite"
			if err := export.WriteSQLite(p, true, meta, rows); err != nil {
				return out, err
			}
			out.Files = append(out.Files, p)
		}
	}
	out.Count = len(rows)
	return out, nil
}
