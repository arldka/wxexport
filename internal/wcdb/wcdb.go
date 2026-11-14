// Package wcdb reads the decrypted plaintext WeChat databases: contacts, sessions, and one
// contact's messages merged across message shards, per docs/SPEC.md §M.
package wcdb

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

// pseudoContacts are WeChat's built-in non-person usernames (docs/FORMAT.md).
var pseudoContacts = map[string]bool{
	"weixin": true, "medianote": true, "fmessage": true, "notifymessage": true,
	"floatbottle": true, "qqmail": true, "brandservicesessionholder": true,
	"brandsessionholder": true, "filehelper": true,
}

// Is1to1 reports whether username should be offered as a 1:1 conversation, per the calibrated
// filter in docs/FORMAT.md (local_type does not distinguish a person from an official account).
func Is1to1(username string) bool {
	if strings.HasSuffix(username, "@chatroom") {
		return false
	}
	if strings.HasPrefix(username, "gh_") {
		return false
	}
	return !pseudoContacts[username]
}

// ShardTable returns the per-contact message table name: "Msg_" + md5hex(username).
func ShardTable(username string) string {
	sum := md5.Sum([]byte(username))
	return "Msg_" + hex.EncodeToString(sum[:])
}

// Contact is one row of contact.db's contact table (the columns this tool uses).
type Contact struct {
	Username, Alias, Remark, NickName string
	LocalType                         int64
}

// DisplayName applies the precedence remark > nick_name > alias > username.
func (c Contact) DisplayName() string {
	for _, s := range []string{c.Remark, c.NickName, c.Alias, c.Username} {
		if s != "" {
			return s
		}
	}
	return c.Username
}

// ContactSummary adds session info (if any) to a Contact, for listing/ordering.
type ContactSummary struct {
	Contact
	LastTimestamp int64
	Summary       string
}

// shard is one open message_N.db.
type shard struct {
	rel string
	db  *sql.DB
}

// Store wraps the decrypted plaintext copies of one account's databases. Callers must Close it.
type Store struct {
	MyWxid    string
	contactDB *sql.DB
	sessionDB *sql.DB
	shards    []shard
}

// Open opens the plaintext files given by rel path (e.g. "contact/contact.db",
// "session/session.db", "message/message_0.db", ...), as produced by package snapshot.
// Unknown rel paths are ignored; missing contact/session DBs are tolerated (Store still works
// for message access, just returns no contacts).
func Open(myWxid string, plainPaths map[string]string) (*Store, error) {
	s := &Store{MyWxid: myWxid}
	open := func(path string) (*sql.DB, error) {
		return sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	}
	for rel, path := range plainPaths {
		switch {
		case rel == "contact/contact.db":
			db, err := open(path)
			if err != nil {
				return nil, fmt.Errorf("wcdb: open %s: %w", rel, err)
			}
			s.contactDB = db
		case rel == "session/session.db":
			db, err := open(path)
			if err != nil {
				return nil, fmt.Errorf("wcdb: open %s: %w", rel, err)
			}
			s.sessionDB = db
		case strings.HasPrefix(rel, "message/message_") && strings.HasSuffix(rel, ".db") &&
			!strings.Contains(rel, "fts") && !strings.Contains(rel, "resource"):
			db, err := open(path)
			if err != nil {
				s.Close()
				return nil, fmt.Errorf("wcdb: open %s: %w", rel, err)
			}
			s.shards = append(s.shards, shard{rel: rel, db: db})
		}
	}
	sort.Slice(s.shards, func(i, j int) bool { return s.shards[i].rel < s.shards[j].rel })
	return s, nil
}

func (s *Store) Close() error {
	var err error
	if s.contactDB != nil {
		err = s.contactDB.Close()
	}
	if s.sessionDB != nil {
		if e := s.sessionDB.Close(); err == nil {
			err = e
		}
	}
	for _, sh := range s.shards {
		if e := sh.db.Close(); err == nil {
			err = e
		}
	}
	return err
}

// Contacts lists conversations. If showAll is false, Is1to1 filters out groups/official
// accounts/pseudo-contacts (the default). Results are ordered by last session activity
// (contacts with no session sort last, alphabetically).
func (s *Store) Contacts(showAll bool) ([]ContactSummary, error) {
	var all []Contact
	if s.contactDB != nil {
		rows, err := s.contactDB.Query(`SELECT username, local_type, alias, remark, nick_name FROM contact`)
		if err != nil {
			return nil, fmt.Errorf("wcdb: query contacts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var c Contact
			if err := rows.Scan(&c.Username, &c.LocalType, &c.Alias, &c.Remark, &c.NickName); err != nil {
				return nil, err
			}
			all = append(all, c)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	sessions := map[string]struct {
		ts      int64
		summary string
	}{}
	if s.sessionDB != nil {
		rows, err := s.sessionDB.Query(`SELECT username, last_timestamp, summary FROM SessionTable`)
		if err != nil {
			return nil, fmt.Errorf("wcdb: query sessions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var u, sm string
			var ts int64
			if err := rows.Scan(&u, &ts, &sm); err != nil {
				return nil, err
			}
			sessions[u] = struct {
				ts      int64
				summary string
			}{ts, sm}
			if seenUsernames := usernameSetFrom(all); !seenUsernames[u] {
				// A session with no contact row (seen in practice, e.g. some @openim/system
				// entries): still worth listing if it passes the 1:1 filter.
				all = append(all, Contact{Username: u})
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	out := make([]ContactSummary, 0, len(all))
	for _, c := range all {
		if !showAll && !Is1to1(c.Username) {
			continue
		}
		cs := ContactSummary{Contact: c}
		if sinfo, ok := sessions[c.Username]; ok {
			cs.LastTimestamp, cs.Summary = sinfo.ts, sinfo.summary
		}
		out = append(out, cs)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastTimestamp != out[j].LastTimestamp {
			return out[i].LastTimestamp > out[j].LastTimestamp
		}
		return out[i].DisplayName() < out[j].DisplayName()
	})
	return out, nil
}

func usernameSetFrom(cs []Contact) map[string]bool {
	m := make(map[string]bool, len(cs))
	for _, c := range cs {
		m[c.Username] = true
	}
	return m
}

// TypeCount is a (type,sub) histogram bucket, for `debug types`.
type TypeCount struct {
	Type, Sub int64
	Count     int
	Sample    string // one example of the (decompressed) content, truncated
}

// TypeHistogram summarizes the (type,sub) distribution of one contact's messages, for
// calibrating/debugging the decoder against real data without exporting anything.
func (s *Store) TypeHistogram(contactUsername string) ([]TypeCount, error) {
	msgs, err := s.Messages(contactUsername, true)
	if err != nil {
		return nil, err
	}
	buckets := map[[2]int64]*TypeCount{}
	var order [][2]int64
	for _, m := range msgs {
		key := [2]int64{m.Decoded.Type, m.Decoded.Sub}
		b, ok := buckets[key]
		if !ok {
			b = &TypeCount{Type: key[0], Sub: key[1], Sample: truncate(m.RawContent, 200)}
			buckets[key] = b
			order = append(order, key)
		}
		b.Count++
	}
	sort.Slice(order, func(i, j int) bool {
		return buckets[order[i]].Count > buckets[order[j]].Count
	})
	out := make([]TypeCount, len(order))
	for i, k := range order {
		out[i] = *buckets[k]
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
