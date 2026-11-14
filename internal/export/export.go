package export

import (
	"time"

	"github.com/arldka/wxexport/internal/wcdb"
)

// Meta describes one export run (docs/SPEC.md §E).
type Meta struct {
	ToolVersion           string         `json:"tool_version"`
	ExportedAt            time.Time      `json:"exported_at"`
	Account               string         `json:"account"`
	Contact               ContactMeta    `json:"contact"`
	TZ                    string         `json:"tz"`
	Filters               FilterMeta     `json:"filters"`
	CountsByKind          map[string]int `json:"counts_by_kind"`
	UnknownDirectionCount int            `json:"unknown_direction_count"`
	WeChatVersion         string         `json:"wechat_version,omitempty"`
}

type ContactMeta struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

type FilterMeta struct {
	From  string   `json:"from,omitempty"`
	To    string   `json:"to,omitempty"`
	Kinds []string `json:"kinds,omitempty"`
}

// BuildRows converts and filters a contact's merged messages into export Rows, in order, and
// computes the Meta counters alongside (so callers get both in one pass).
func BuildRows(messages []wcdb.Message, loc *time.Location, includeRaw bool, f Filter) ([]Row, map[string]int, int) {
	rows := make([]Row, 0, len(messages))
	counts := map[string]int{}
	unknownDir := 0
	for _, m := range messages {
		r := BuildRow(m, loc, includeRaw)
		if !f.Match(r) {
			continue
		}
		rows = append(rows, r)
		counts[r.Kind]++
		if r.Direction == string(wcdb.DirUnknown) {
			unknownDir++
		}
	}
	return rows, counts, unknownDir
}
