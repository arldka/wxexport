package export

import (
	"time"
)

// Filter selects which rows to export: an inclusive date range (in the export tz) and a set of
// kinds (empty/nil = all kinds).
type Filter struct {
	From, To *time.Time // inclusive, in the export tz; nil = unbounded
	Kinds    map[string]bool
}

// Match reports whether a row (already built with the export tz) passes the filter.
func (f Filter) Match(r Row) bool {
	if len(f.Kinds) > 0 && !f.Kinds[r.Kind] {
		return false
	}
	if f.From != nil && r.TimestampUnix < f.From.Unix() {
		return false
	}
	if f.To != nil && r.TimestampUnix > f.To.Unix() {
		return false
	}
	return true
}

// DayRange parses "YYYY-MM-DD" (from) and "YYYY-MM-DD" (to) in loc into an inclusive [from,
// to] *time.Time pair suitable for Filter, where `to` is pushed to the end of that day. Empty
// strings mean unbounded on that side.
func DayRange(from, to string, loc *time.Location) (*time.Time, *time.Time, error) {
	var f, t *time.Time
	if from != "" {
		v, err := time.ParseInLocation("2006-01-02", from, loc)
		if err != nil {
			return nil, nil, err
		}
		f = &v
	}
	if to != "" {
		v, err := time.ParseInLocation("2006-01-02", to, loc)
		if err != nil {
			return nil, nil, err
		}
		v = v.Add(24*time.Hour - time.Second)
		t = &v
	}
	return f, t, nil
}
