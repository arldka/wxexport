package wcdb

import (
	"bytes"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"
)

const myWxid = "wxid_me"
const contactA = "wxid_alice"

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// buildFixture creates a tiny but realistic account: contact.db, session.db, and two message
// shards whose Msg_<md5> table for contactA holds overlapping (deduped) and distinct rows, plus
// a zstd-compressed row and a row missing an optional column (via a narrower shard schema).
func buildFixture(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	open := func(name string) *sql.DB {
		db, err := sql.Open("sqlite", filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return db
	}

	contactDB := open("contact.db")
	mustExec(t, contactDB, `CREATE TABLE contact(username TEXT, local_type INTEGER, alias TEXT, remark TEXT, nick_name TEXT)`)
	mustExec(t, contactDB, `INSERT INTO contact VALUES (?,1,'','Friend Alice','Alice')`, contactA)
	mustExec(t, contactDB, `INSERT INTO contact VALUES ('gh_official',1,'','','Official Account')`)
	mustExec(t, contactDB, `INSERT INTO contact VALUES ('weixin',1,'','','WeChat Team')`)
	mustExec(t, contactDB, `INSERT INTO contact VALUES ('12345@chatroom',2,'','','Some Group')`)
	contactDB.Close()

	sessionDB := open("session.db")
	mustExec(t, sessionDB, `CREATE TABLE SessionTable(username TEXT PRIMARY KEY, last_timestamp INTEGER, summary TEXT)`)
	mustExec(t, sessionDB, `INSERT INTO SessionTable VALUES (?, 1700000500, 'hi there')`, contactA)
	sessionDB.Close()

	table := ShardTable(contactA)

	shard0 := open("message_0.db")
	mustExec(t, shard0, `CREATE TABLE Name2Id(user_name TEXT PRIMARY KEY, is_session INTEGER)`)
	mustExec(t, shard0, `INSERT INTO Name2Id(user_name) VALUES (?)`, contactA) // rowid 1
	mustExec(t, shard0, `INSERT INTO Name2Id(user_name) VALUES (?)`, myWxid)   // rowid 2
	mustExec(t, shard0, `CREATE TABLE `+quoted(table)+`(local_id INTEGER PRIMARY KEY, server_id INTEGER,
		local_type INTEGER, sort_seq INTEGER, real_sender_id INTEGER, create_time INTEGER,
		source TEXT, message_content TEXT, WCDB_CT_message_content INTEGER, WCDB_CT_source INTEGER)`)
	// row 1: plain text from the contact, server_id=100 (will also appear, duplicated, in shard1)
	mustExec(t, shard0, `INSERT INTO `+quoted(table)+` VALUES (1,100,1,10,1,1700000100,NULL,'hello from alice',0,0)`)
	// row 2: text from me, zstd-compressed content
	mustExec(t, shard0, `INSERT INTO `+quoted(table)+` VALUES (2,101,1,20,2,1700000200,NULL,?,4,0)`, zstdOf(t, "hi alice, compressed"))
	shard0.Close()

	shard1 := open("message_1.db")
	mustExec(t, shard1, `CREATE TABLE Name2Id(user_name TEXT PRIMARY KEY, is_session INTEGER)`)
	// Note the different rowid order in this shard: real_sender_id is only meaningful within the
	// same db file, which this test exercises (contactA is rowid 1 here too, by coincidence of
	// insertion order, but the point is each shard resolves independently).
	mustExec(t, shard1, `INSERT INTO Name2Id(user_name) VALUES (?)`, contactA) // rowid 1
	// Narrower schema: no sort_seq, no WCDB_CT_source column at all (graceful degradation check).
	mustExec(t, shard1, `CREATE TABLE `+quoted(table)+`(local_id INTEGER PRIMARY KEY, server_id INTEGER,
		local_type INTEGER, real_sender_id INTEGER, create_time INTEGER, message_content TEXT,
		WCDB_CT_message_content INTEGER)`)
	// duplicate of shard0's row 1 (same server_id+create_time+type): must be deduped
	mustExec(t, shard1, `INSERT INTO `+quoted(table)+` VALUES (1,100,1,1,1700000100,'hello from alice (dup copy)',0)`)
	// a call message, unique to this shard
	mustExec(t, shard1, `INSERT INTO `+quoted(table)+` VALUES (2,102,50,1,1700000300,'Canceled',0)`)
	shard1.Close()

	// An unrelated shard with no table for this contact at all (must be skipped, not error).
	shard2 := open("message_2.db")
	mustExec(t, shard2, `CREATE TABLE Name2Id(user_name TEXT PRIMARY KEY, is_session INTEGER)`)
	shard2.Close()

	return map[string]string{
		"contact/contact.db":     filepath.Join(dir, "contact.db"),
		"session/session.db":     filepath.Join(dir, "session.db"),
		"message/message_0.db":   filepath.Join(dir, "message_0.db"),
		"message/message_1.db":   filepath.Join(dir, "message_1.db"),
		"message/message_2.db":   filepath.Join(dir, "message_2.db"),
		"message/message_fts.db": filepath.Join(dir, "message_0.db"), // must be excluded by name
	}
}

func quoted(s string) string { return `"` + s + `"` }

func zstdOf(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestIs1to1Filter(t *testing.T) {
	cases := map[string]bool{
		"wxid_alice": true, "gh_official": false, "weixin": false,
		"12345@chatroom": false, "10000000000000001@openim": true, "legacyname123": true,
	}
	for u, want := range cases {
		if got := Is1to1(u); got != want {
			t.Errorf("Is1to1(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestShardTableIsMD5(t *testing.T) {
	for _, u := range []string{"wxid_alice", "x", "wxid_bijy_example"} {
		sum := md5.Sum([]byte(u))
		want := "Msg_" + hex.EncodeToString(sum[:])
		if got := ShardTable(u); got != want {
			t.Errorf("ShardTable(%q) = %q, want %q", u, got, want)
		}
	}
}

func TestContactsFilteredAndOrdered(t *testing.T) {
	store, err := Open(myWxid, buildFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cs, err := store.Contacts(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Username != contactA {
		t.Fatalf("filtered contacts = %+v", cs)
	}
	if cs[0].DisplayName() != "Friend Alice" {
		t.Errorf("display name = %q", cs[0].DisplayName())
	}
	if cs[0].LastTimestamp != 1700000500 || cs[0].Summary != "hi there" {
		t.Errorf("session info = %+v", cs[0])
	}

	all, err := store.Contacts(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("unfiltered contacts = %d, want 4: %+v", len(all), all)
	}
}

func TestMessagesMergeDedupAndDecode(t *testing.T) {
	store, err := Open(myWxid, buildFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	msgs, err := store.Messages(contactA, true)
	if err != nil {
		t.Fatal(err)
	}
	// 2 rows in shard0 + 1 unique + 1 deduped-away in shard1 = 3 total.
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(msgs), msgs)
	}

	// Ordered by create_time.
	if msgs[0].TimestampUnix != 1700000100 || msgs[1].TimestampUnix != 1700000200 || msgs[2].TimestampUnix != 1700000300 {
		t.Fatalf("not ordered by time: %+v", msgs)
	}

	// Row 0: from alice (shard0's copy won, since it's scanned first and dedup keeps the first).
	if msgs[0].Direction != DirReceived || msgs[0].SenderUsername != contactA {
		t.Errorf("row0 direction = %+v", msgs[0])
	}
	if msgs[0].RawContent != "hello from alice" {
		t.Errorf("row0 content = %q (dedup should have kept shard0's copy)", msgs[0].RawContent)
	}

	// Row 1: from me, zstd-decompressed.
	if msgs[1].Direction != DirSent || msgs[1].SenderUsername != myWxid {
		t.Errorf("row1 direction = %+v", msgs[1])
	}
	if msgs[1].RawContent != "hi alice, compressed" {
		t.Errorf("row1 content = %q", msgs[1].RawContent)
	}

	// Row 2: the call, from shard1's narrower schema (no sort_seq/WCDB_CT_source columns).
	if msgs[2].Decoded.Kind != "call" || msgs[2].Shard != "message/message_1.db" {
		t.Errorf("row2 = %+v", msgs[2])
	}
}

func TestMessagesUnknownContactReturnsEmpty(t *testing.T) {
	store, err := Open(myWxid, buildFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msgs, err := store.Messages("wxid_nobody", false)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("msgs=%v err=%v", msgs, err)
	}
}

func TestTypeHistogram(t *testing.T) {
	store, err := Open(myWxid, buildFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hist, err := store.TypeHistogram(contactA)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, h := range hist {
		total += h.Count
	}
	if total != 3 {
		t.Fatalf("histogram total = %d, want 3: %+v", total, hist)
	}
}

func TestOpenIgnoresUnrelatedFiles(t *testing.T) {
	paths := buildFixture(t)
	paths["db_storage/weclaw.db"] = paths["message/message_0.db"] // arbitrary extra, must be ignored
	store, err := Open(myWxid, paths)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if len(store.shards) != 3 { // message_0, message_1, message_2 (fts excluded)
		t.Errorf("shards = %d, want 3", len(store.shards))
	}
}
