package export

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/arldka/wxexport/internal/msg"
	"github.com/arldka/wxexport/internal/wcdb"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("tz database %q unavailable: %v", name, err)
	}
	return loc
}

// fixtureMessages builds a small, deterministic message set: one text each day for 3 days
// (around a UTC midnight boundary that differs by date in Shanghai vs Paris), one call, one
// image, and one message with unknown direction.
func fixtureMessages() []wcdb.Message {
	// 2024-01-15T23:30:00Z: 2024-01-16 in Asia/Shanghai (+8), 2024-01-16 00:30 in Europe/Paris...
	// actually Paris is UTC+1 in January, so 2024-01-16T00:30 Paris too. Pick a boundary where
	// they genuinely differ: 2024-01-15T22:30:00Z -> Shanghai 2024-01-16 06:30, Paris 2024-01-15 23:30.
	boundary := time.Date(2024, 1, 15, 22, 30, 0, 0, time.UTC).Unix()
	return []wcdb.Message{
		{LocalID: 1, ServerID: 10, Shard: "message/message_0.db", ContactUsername: "wxid_alice",
			SenderUsername: "wxid_alice", Direction: wcdb.DirReceived, TimestampUnix: boundary,
			Decoded: msg.Decode(1, "hello", "")},
		{LocalID: 2, ServerID: 11, Shard: "message/message_0.db", ContactUsername: "wxid_alice",
			SenderUsername: "wxid_me", Direction: wcdb.DirSent, TimestampUnix: boundary + 3600,
			Decoded: msg.Decode(3, `<msg><img md5="abc123" length="500" /></msg>`, "")},
		{LocalID: 3, ServerID: 12, Shard: "message/message_0.db", ContactUsername: "wxid_alice",
			SenderUsername: "", Direction: wcdb.DirUnknown, TimestampUnix: boundary + 7200,
			Decoded: msg.Decode(10000, "system note", "")},
		{LocalID: 4, ServerID: 13, Shard: "message/message_0.db", ContactUsername: "wxid_alice",
			SenderUsername: "wxid_alice", Direction: wcdb.DirReceived, TimestampUnix: boundary + 86400,
			Decoded: msg.Decode(50, `<voipmsg type="VoIPBubbleMsg"><VoIPBubbleMsg><msg><![CDATA[Duration: 01:30]]></msg><room_type>0</room_type></VoIPBubbleMsg></voipmsg>`, "")},
	}
}

func TestBuildRowTZBoundary(t *testing.T) {
	sh := mustLoc(t, "Asia/Shanghai")
	pa := mustLoc(t, "Europe/Paris")
	m := fixtureMessages()[0]

	rSh := BuildRow(m, sh, false)
	rPa := BuildRow(m, pa, false)
	if rSh.Date == rPa.Date {
		t.Fatalf("expected different dates across tz for the same instant, got %q both", rSh.Date)
	}
	if rSh.Date != "2024-01-16" || rPa.Date != "2024-01-15" {
		t.Errorf("sh=%s pa=%s", rSh.Date, rPa.Date)
	}
}

func TestBuildRowFieldsByKind(t *testing.T) {
	loc := time.UTC
	msgs := fixtureMessages()
	rows := make([]Row, len(msgs))
	for i, m := range msgs {
		rows[i] = BuildRow(m, loc, false)
	}

	if rows[0].Kind != "text" || rows[0].CharCount != 5 {
		t.Errorf("row0 = %+v", rows[0])
	}
	if !rows[1].IsPicture || rows[1].MediaMD5 != "abc123" || rows[1].MediaSize != 500 {
		t.Errorf("row1 = %+v", rows[1])
	}
	if rows[2].Direction != "unknown" {
		t.Errorf("row2 direction = %q", rows[2].Direction)
	}
	if !rows[3].IsCall() || rows[3].CallOutcome != "completed" || rows[3].CallDurationS != 90 {
		t.Errorf("row3 = %+v", rows[3])
	}
}

func TestFilterKindsAndDateRange(t *testing.T) {
	loc := time.UTC
	msgs := fixtureMessages()
	from, to, err := DayRange("2024-01-16", "2024-01-16", loc)
	if err != nil {
		t.Fatal(err)
	}
	rows, counts, unknownDir := BuildRows(msgs, loc, false, Filter{From: from, To: to})
	// Only row2 (system, 00:30 UTC) and row3 (call, 22:30 UTC) fall on 2024-01-16 UTC; row0/row1
	// (22:30/23:30 UTC on the 15th) do not.
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}
	if counts["system"] != 1 || counts["call"] != 1 {
		t.Errorf("counts = %+v", counts)
	}
	if unknownDir != 1 {
		t.Errorf("unknownDir = %d", unknownDir)
	}

	kindsOnly, _, _ := BuildRows(msgs, loc, false, Filter{Kinds: map[string]bool{"call": true}})
	if len(kindsOnly) != 1 || !kindsOnly[0].IsCall() {
		t.Fatalf("kind filter: %+v", kindsOnly)
	}
}

func baseMeta() Meta {
	return Meta{
		ToolVersion: "test", ExportedAt: time.Date(2024, 1, 20, 0, 0, 0, 0, time.UTC),
		Account: "wxid_me", Contact: ContactMeta{Username: "wxid_alice", DisplayName: "Alice"}, TZ: "UTC",
	}
}

func TestWriteJSONGolden(t *testing.T) {
	loc := time.UTC
	rows, counts, unknownDir := BuildRows(fixtureMessages(), loc, false, Filter{})
	meta := baseMeta()
	meta.CountsByKind, meta.UnknownDirectionCount = counts, unknownDir

	dst := filepath.Join(t.TempDir(), "out.json")
	if err := WriteJSON(dst, false, meta, rows); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	var doc jsonDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(doc.Messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(doc.Messages))
	}
	if len(doc.Calls) != 1 || !doc.Calls[0].IsCall() {
		t.Fatalf("calls subset = %+v", doc.Calls)
	}
	if doc.Meta.Contact.DisplayName != "Alice" || doc.Meta.CountsByKind["text"] != 1 {
		t.Fatalf("meta = %+v", doc.Meta)
	}
	if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v", fi.Mode().Perm())
	}
}

func TestWriteCSVGolden(t *testing.T) {
	loc := time.UTC
	rows, _, _ := BuildRows(fixtureMessages(), loc, false, Filter{})
	dir := t.TempDir()
	msgsPath := filepath.Join(dir, "messages.csv")
	callsPath := filepath.Join(dir, "calls.csv")
	if err := WriteMessagesCSV(msgsPath, false, false, false, rows); err != nil {
		t.Fatal(err)
	}
	if err := WriteCallsCSV(callsPath, false, false, rows); err != nil {
		t.Fatal(err)
	}

	mf, err := os.Open(msgsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer mf.Close()
	records, err := csv.NewReader(mf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 5 { // header + 4 rows
		t.Fatalf("messages.csv rows = %d, want 5", len(records))
	}
	if records[0][0] != "local_id" || records[0][len(records[0])-1] != "title" {
		t.Errorf("header = %v", records[0])
	}

	cf, err := os.Open(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer cf.Close()
	callRecords, err := csv.NewReader(cf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(callRecords) != 2 { // header + 1 call
		t.Fatalf("calls.csv rows = %d, want 2: %v", len(callRecords), callRecords)
	}
	if callRecords[1][6] != "completed" || callRecords[1][7] != "90" {
		t.Errorf("call row = %v", callRecords[1])
	}
}

func TestWriteCSVExcelBOM(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "m.csv")
	if err := WriteMessagesCSV(dst, false, false, true, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dst)
	if len(b) < 3 || b[0] != 0xEF || b[1] != 0xBB || b[2] != 0xBF {
		t.Errorf("missing BOM: % x", b[:min(6, len(b))])
	}
}

func TestWriteCSVIncludeRaw(t *testing.T) {
	loc := time.UTC
	rows, _, _ := BuildRows(fixtureMessages()[:1], loc, true, Filter{})
	dst := filepath.Join(t.TempDir(), "m.csv")
	if err := WriteMessagesCSV(dst, false, true, false, rows); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(dst)
	defer f.Close()
	records, _ := csv.NewReader(f).ReadAll()
	if records[0][len(records[0])-1] != "raw_content" {
		t.Errorf("header missing raw_content: %v", records[0])
	}
}

func TestAtomicWriteNoPartialFileOnError(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.txt")
	err := WriteFileAtomic(dst, false, func(f *os.File) error {
		f.WriteString("partial")
		return os.ErrInvalid // simulate a failure mid-write
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("destination must not exist after a failed write")
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file must be cleaned up after a failed write")
	}
}

func TestAtomicWriteRefusesOverwriteWithoutForce(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "out.txt")
	write := func(f *os.File) error { _, err := f.WriteString("x"); return err }
	if err := WriteFileAtomic(dst, false, write); err != nil {
		t.Fatal(err)
	}
	err := WriteFileAtomic(dst, false, write)
	if _, ok := err.(*ErrExists); !ok {
		t.Fatalf("got %T: %v, want *ErrExists", err, err)
	}
	if err := WriteFileAtomic(dst, true, func(f *os.File) error { _, err := f.WriteString("y"); return err }); err != nil {
		t.Fatalf("force overwrite should succeed: %v", err)
	}
	b, _ := os.ReadFile(dst)
	if string(b) != "y" {
		t.Errorf("content = %q, want y", b)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestWriteSQLiteSchemaAndAnalyticsQuery re-opens the written SQLite export and checks the
// schema/indexes, then runs the example "messages per day" and "call minutes per week" queries
// the README documents (docs/SPEC.md §E).
func TestWriteSQLiteSchemaAndAnalyticsQuery(t *testing.T) {
	loc := time.UTC
	rows, counts, unknownDir := BuildRows(fixtureMessages(), loc, false, Filter{})
	meta := baseMeta()
	meta.CountsByKind, meta.UnknownDirectionCount = counts, unknownDir

	dst := filepath.Join(t.TempDir(), "alice.sqlite")
	if err := WriteSQLite(dst, false, meta, rows); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dst); err != nil || fi.Size() == 0 {
		t.Fatalf("sqlite file missing or empty: %v", err)
	}
	// Writing again without force must fail and must not have touched the file.
	before, _ := os.ReadFile(dst)
	if err := WriteSQLite(dst, false, meta, rows); err == nil {
		t.Fatal("expected ErrExists")
	}
	after, _ := os.ReadFile(dst)
	if string(before) != string(after) {
		t.Fatal("file was modified despite refused overwrite")
	}

	db, err := sql.Open("sqlite", dst+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM messages`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("messages count = %d, err=%v", n, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM calls`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("calls count = %d, err=%v", n, err)
	}
	var idxCount int
	db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN
		('messages_date','messages_iso_week','messages_kind','messages_direction','calls_date','calls_iso_week')`).Scan(&idxCount)
	if idxCount != 6 {
		t.Fatalf("index count = %d, want 6", idxCount)
	}

	// README example: messages per day.
	dayRows, err := db.Query(`SELECT date, count(*) FROM messages GROUP BY date ORDER BY date`)
	if err != nil {
		t.Fatal(err)
	}
	defer dayRows.Close()
	perDay := map[string]int{}
	for dayRows.Next() {
		var d string
		var c int
		dayRows.Scan(&d, &c)
		perDay[d] = c
	}
	if perDay["2024-01-15"] != 2 || perDay["2024-01-16"] != 2 {
		t.Fatalf("per-day counts = %+v", perDay)
	}

	// README example: call minutes per ISO week.
	var minutes float64
	if err := db.QueryRow(`SELECT sum(call_duration_s)/60.0 FROM calls`).Scan(&minutes); err != nil {
		t.Fatal(err)
	}
	if minutes != 1.5 {
		t.Errorf("call minutes = %v, want 1.5", minutes)
	}

	var metaJSON string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key='export'`).Scan(&metaJSON); err != nil {
		t.Fatal(err)
	}
	var gotMeta Meta
	if err := json.Unmarshal([]byte(metaJSON), &gotMeta); err != nil || gotMeta.Contact.DisplayName != "Alice" {
		t.Fatalf("meta row: %v %+v", err, gotMeta)
	}
}
