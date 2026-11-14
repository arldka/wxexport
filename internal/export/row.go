// Package export writes a contact's messages to JSON, CSV and/or SQLite, per docs/SPEC.md §E.
package export

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/arldka/wxexport/internal/msg"
	"github.com/arldka/wxexport/internal/wcdb"
)

// Row is one message, flattened to the export columns listed in docs/SPEC.md §M/§E.
type Row struct {
	LocalID         int64   `json:"local_id"`
	ServerID        int64   `json:"server_id"`
	Shard           string  `json:"shard"`
	ContactUsername string  `json:"contact_username"`
	SenderUsername  string  `json:"sender_username"`
	Direction       string  `json:"direction"`
	TimestampUnix   int64   `json:"timestamp_unix"`
	TimestampISO    string  `json:"timestamp_iso"`
	Date            string  `json:"date"`     // YYYY-MM-DD in the export tz
	ISOWeek         string  `json:"iso_week"` // YYYY-Www in the export tz
	Hour            int     `json:"hour"`     // 0-23 in the export tz
	Type            int64   `json:"type"`
	SubType         int64   `json:"sub_type"`
	Kind            string  `json:"kind"`
	Text            string  `json:"text,omitempty"`
	CharCount       int     `json:"char_count,omitempty"`
	IsPicture       bool    `json:"is_picture,omitempty"`
	MediaMD5        string  `json:"media_md5,omitempty"`
	MediaSize       int64   `json:"media_size,omitempty"`
	MediaDurationS  float64 `json:"media_duration_s,omitempty"`
	CallMedia       string  `json:"call_media,omitempty"`
	CallOutcome     string  `json:"call_outcome,omitempty"`
	CallDurationS   int64   `json:"call_duration_s,omitempty"`
	URL             string  `json:"url,omitempty"`
	Title           string  `json:"title,omitempty"`
	RawContent      string  `json:"raw_content,omitempty"`
}

// IsCall reports whether this row is a call message (used to build calls.csv / meta.calls).
func (r Row) IsCall() bool { return r.Kind == string(msg.KindCall) }

// BuildRow flattens one wcdb.Message into a Row, computing date/iso_week/hour in loc.
// includeRaw controls whether RawContent is populated.
func BuildRow(m wcdb.Message, loc *time.Location, includeRaw bool) Row {
	t := time.Unix(m.TimestampUnix, 0).In(loc)
	isoYear, isoWeek := t.ISOWeek()

	r := Row{
		LocalID:         m.LocalID,
		ServerID:        m.ServerID,
		Shard:           m.Shard,
		ContactUsername: m.ContactUsername,
		SenderUsername:  m.SenderUsername,
		Direction:       string(m.Direction),
		TimestampUnix:   m.TimestampUnix,
		TimestampISO:    t.Format(time.RFC3339),
		Date:            t.Format("2006-01-02"),
		ISOWeek:         fmt.Sprintf("%04d-W%02d", isoYear, isoWeek),
		Hour:            t.Hour(),
		Type:            m.Decoded.Type,
		SubType:         m.Decoded.Sub,
		Kind:            string(m.Decoded.Kind),
		Text:            m.Decoded.Text,
	}
	if r.Text != "" {
		r.CharCount = utf8.RuneCountInString(r.Text)
	}
	if includeRaw {
		r.RawContent = m.RawContent
	}

	d := m.Decoded
	switch d.Kind {
	case msg.KindImage:
		r.IsPicture = true
		fillMedia(&r, d.Media, 0)
	case msg.KindSticker:
		fillMedia(&r, d.Media, 0)
	case msg.KindVoice:
		if d.Media != nil {
			r.MediaDurationS = float64(d.Media.DurationMS) / 1000
		}
	case msg.KindVideo:
		fillMedia(&r, d.Media, float64(mediaDurationS(d.Media)))
	case msg.KindLocation:
		if d.Location != nil {
			r.Title = d.Location.Label
		}
	case msg.KindLink:
		if d.Link != nil {
			r.Title, r.URL = d.Link.Title, d.Link.URL
		}
	case msg.KindFile:
		if d.File != nil {
			r.Title = d.File.Title
			r.MediaSize = d.File.SizeBytes
		}
	case msg.KindQuote, msg.KindForward, msg.KindApplet, msg.KindTransfer, msg.KindApp:
		r.Title = firstNonEmpty(d.AppTitle, d.Text)
	case msg.KindCall:
		if d.Call != nil {
			r.CallMedia = string(d.Call.Media)
			r.CallOutcome = string(d.Call.Outcome)
			r.CallDurationS = d.Call.DurationS
		}
	}
	return r
}

func mediaDurationS(m *msg.Media) int64 {
	if m == nil {
		return 0
	}
	return m.DurationS
}

func fillMedia(r *Row, m *msg.Media, durationS float64) {
	if m == nil {
		return
	}
	r.MediaMD5 = m.MD5
	r.MediaSize = m.SizeBytes
	if durationS > 0 {
		r.MediaDurationS = durationS
	}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// Columns lists every CSV/struct column in a fixed, documented order (docs/SPEC.md §M), with
// raw_content last and only emitted by callers that asked for it.
var Columns = []string{
	"local_id", "server_id", "shard", "contact_username", "sender_username", "direction",
	"timestamp_unix", "timestamp_iso", "date", "iso_week", "hour", "type", "sub_type", "kind",
	"text", "char_count", "is_picture", "media_md5", "media_size", "media_duration_s",
	"call_media", "call_outcome", "call_duration_s", "url", "title",
}

// CallColumns lists the calls.csv column order (docs/SPEC.md §E).
var CallColumns = []string{
	"local_id", "timestamp_iso", "date", "iso_week", "direction", "call_media", "call_outcome", "call_duration_s",
}

// Values returns r's values in Columns order (and raw_content appended if includeRaw), as
// strings suitable for CSV. Booleans are "true"/"false"; zero numeric fields with omitempty
// semantics are rendered empty when they represent "not applicable" rather than a real zero.
func (r Row) Values(includeRaw bool) []string {
	out := []string{
		itoa(r.LocalID), itoa(r.ServerID), r.Shard, r.ContactUsername, r.SenderUsername, r.Direction,
		itoa(r.TimestampUnix), r.TimestampISO, r.Date, r.ISOWeek, itoa(int64(r.Hour)), itoa(r.Type), itoa(r.SubType), r.Kind,
		r.Text, intOrEmpty(r.CharCount), boolStr(r.IsPicture), r.MediaMD5, intOrEmptyI64(r.MediaSize), floatOrEmpty(r.MediaDurationS),
		r.CallMedia, r.CallOutcome, intOrEmptyI64NonCall(r), r.URL, r.Title,
	}
	if includeRaw {
		out = append(out, r.RawContent)
	}
	return out
}

// CallValues returns r's calls.csv row (CallColumns order). Caller must check r.IsCall() first.
func (r Row) CallValues() []string {
	return []string{itoa(r.LocalID), r.TimestampISO, r.Date, r.ISOWeek, r.Direction, r.CallMedia, r.CallOutcome, itoa(r.CallDurationS)}
}

func itoa(n int64) string { return fmt.Sprintf("%d", n) }
func intOrEmpty(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}
func intOrEmptyI64(n int64) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}
func intOrEmptyI64NonCall(r Row) string {
	if r.Kind != string(msg.KindCall) {
		return ""
	}
	return fmt.Sprintf("%d", r.CallDurationS)
}
func floatOrEmpty(f float64) string {
	if f == 0 {
		return ""
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", f), "0"), ".")
}
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return ""
}
