package wcdb

import (
	"database/sql"
	"fmt"
	"sort"

	"github.com/arldka/wxexport/internal/msg"
)

// Direction classifies who sent a message.
type Direction string

const (
	DirSent     Direction = "sent"
	DirReceived Direction = "received"
	DirUnknown  Direction = "unknown"
)

// Message is one normalized message row, merged across shards for one contact.
type Message struct {
	LocalID, ServerID int64
	Shard             string // rel path of the shard db it came from
	ContactUsername   string
	SenderUsername    string
	Direction         Direction
	TimestampUnix     int64
	SortSeq           int64
	Decoded           msg.Decoded
	RawContent        string // only populated if includeRaw
	RawSource         string
}

// requiredCols are read via PRAGMA table_info so a missing optional column degrades gracefully
// (docs/SPEC.md §M) instead of crashing; local_id/local_type/create_time are load-bearing.
var optionalCols = []string{"server_id", "sort_seq", "real_sender_id", "source",
	"message_content", "compress_content", "WCDB_CT_message_content", "WCDB_CT_source"}

// tableColumns returns the set of columns table actually has in db.
func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// name2IDMap loads rowid -> user_name for a shard's Name2Id table (empty map if absent).
func name2IDMap(db *sql.DB, table string) (map[int64]string, error) {
	cols, err := tableColumns(db, table)
	if err != nil {
		return nil, err
	}
	if !cols["user_name"] { // table doesn't exist in this db (PRAGMA returns no rows then)
		return map[int64]string{}, nil
	}
	rows, err := db.Query(`SELECT rowid, user_name FROM Name2Id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// Messages returns contactUsername's messages merged across every shard, deduped by
// (server_id, create_time, local_type) when server_id != 0, ordered by create_time, sort_seq,
// local_id. includeRaw keeps the decompressed raw content/source on each Message.
func (s *Store) Messages(contactUsername string, includeRaw bool) ([]Message, error) {
	table := ShardTable(contactUsername)
	var all []Message
	seen := map[[3]int64]bool{} // (server_id, create_time, local_type), only when server_id != 0

	for _, sh := range s.shards {
		cols, err := tableColumns(sh.db, table)
		if err != nil {
			return nil, fmt.Errorf("wcdb: %s: table_info(%s): %w", sh.rel, table, err)
		}
		if !cols["local_id"] { // table absent in this shard
			continue
		}
		n2id, err := name2IDMap(sh.db, "Name2Id")
		if err != nil {
			return nil, fmt.Errorf("wcdb: %s: Name2Id: %w", sh.rel, err)
		}

		selectCols := []string{"local_id", "local_type", "create_time"}
		for _, c := range optionalCols {
			if cols[c] {
				selectCols = append(selectCols, c)
			}
		}
		q := fmt.Sprintf(`SELECT %s FROM %q ORDER BY create_time, local_id`, joinCols(selectCols), table)
		rows, err := sh.db.Query(q)
		if err != nil {
			return nil, fmt.Errorf("wcdb: %s: query %s: %w", sh.rel, table, err)
		}
		for rows.Next() {
			vals := make([]any, len(selectCols))
			ptrs := make([]any, len(selectCols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return nil, err
			}
			m, err := buildMessage(contactUsername, sh.rel, selectCols, vals, n2id, s.MyWxid, includeRaw)
			if err != nil {
				rows.Close()
				return nil, err
			}
			if m.ServerID != 0 {
				key := [3]int64{m.ServerID, m.TimestampUnix, m.Decoded.Type}
				if seen[key] {
					continue
				}
				seen[key] = true
			}
			all = append(all, m)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	sort.SliceStable(all, func(i, j int) bool {
		if all[i].TimestampUnix != all[j].TimestampUnix {
			return all[i].TimestampUnix < all[j].TimestampUnix
		}
		if all[i].SortSeq != all[j].SortSeq {
			return all[i].SortSeq < all[j].SortSeq
		}
		return all[i].LocalID < all[j].LocalID
	})
	return all, nil
}

func joinCols(cols []string) string {
	out := ""
	for i, c := range cols {
		if i > 0 {
			out += ", "
		}
		out += `"` + c + `"`
	}
	return out
}

func colIndex(cols []string, name string) int {
	for i, c := range cols {
		if c == name {
			return i
		}
	}
	return -1
}

func asInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	case nil:
		return 0
	default:
		return 0
	}
}

func buildMessage(contact, shardRel string, cols []string, vals []any, n2id map[int64]string, myWxid string, includeRaw bool) (Message, error) {
	get := func(name string) any {
		if i := colIndex(cols, name); i >= 0 {
			return vals[i]
		}
		return nil
	}

	m := Message{
		LocalID:         asInt64(get("local_id")),
		ServerID:        asInt64(get("server_id")),
		Shard:           shardRel,
		ContactUsername: contact,
		TimestampUnix:   asInt64(get("create_time")),
		SortSeq:         asInt64(get("sort_seq")),
	}
	localType := asInt64(get("local_type"))

	if sid := get("real_sender_id"); sid != nil {
		if name, ok := n2id[asInt64(sid)]; ok {
			m.SenderUsername = name
		}
	}
	switch {
	case m.SenderUsername == myWxid && myWxid != "":
		m.Direction = DirSent
	case m.SenderUsername == contact:
		m.Direction = DirReceived
	default:
		m.Direction = DirUnknown
	}

	content, err := decodeCell(get("message_content"), get("WCDB_CT_message_content"))
	if err != nil {
		return Message{}, err
	}
	source, err := decodeCell(get("source"), get("WCDB_CT_source"))
	if err != nil {
		return Message{}, err
	}
	m.Decoded = msg.Decode(localType, content, source)
	if includeRaw {
		m.RawContent, m.RawSource = content, source
	}
	return m, nil
}

func decodeCell(raw, ctFlag any) (string, error) {
	if raw == nil {
		return "", nil
	}
	var b []byte
	switch x := raw.(type) {
	case string:
		b = []byte(x)
	case []byte:
		b = x
	default:
		return "", nil
	}
	flag, valid := int64(0), false
	if ctFlag != nil {
		flag, valid = asInt64(ctFlag), true
	}
	return msg.DecodeContent(b, flag, valid)
}
