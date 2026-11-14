//go:build e2e

// Package e2e drives the compiled wxexport binary as a real subprocess, tying together
// discovery, key resolution, database access, message decoding and export end to end, without
// a real WeChat install or account.
//
// Fixture design note: this deliberately does NOT build a real multi-table SQLite schema and
// push it through the test-only SQLCipher encryptor (internal/sqlcipher/sqlciphertest). A real
// SQLite b-tree page packs cell content from the END of the page backward, so even a tiny
// schema's content legitimately reaches into the last bytes of page 1 — exactly the 80-byte
// region a genuine SQLCipher/WCDB database never touches, because that database was created
// with its usable page size already reduced from the start. Retrofitting an ordinary SQLite
// file into our fixed-reserve encryptor therefore silently truncates real content (confirmed:
// decrypting such a fixture reliably corrupts the tail of page 1). Building a reserve-respecting
// encrypted *and* queryable database would need a real SQLCipher codec (CGO), which the project
// deliberately avoids. So the key mechanics (encrypt/decrypt/HMAC/cache) are exercised here with
// the same non-queryable, reserve-respecting random page content internal/sqlcipher's own unit
// tests use, while contacts/export are exercised against plain (already-decrypted) databases,
// using the pipeline's existing, spec'd pass-through support for unencrypted files
// (keys.StatusPlain / snapshot's "already-plain" branch — see internal/keys, internal/snapshot).
// Decrypting a *real* encrypted WeChat account end to end was verified manually against the
// actual account during development (docs/FORMAT.md, and the git history of each phase).
package e2e

import (
	"bytes"
	"crypto/md5"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/arldka/wxexport/internal/sqlcipher"
	"github.com/arldka/wxexport/internal/sqlcipher/sqlciphertest"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

func wxexportBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir := filepath.Join(os.TempDir(), "wxexport-e2e-bin")
		os.MkdirAll(dir, 0o755)
		binPath = filepath.Join(dir, "wxexport")
		cmd := exec.Command("go", "build", "-o", binPath, "../cmd/wxexport")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = &buildFailure{err, out}
		}
	})
	if buildErr != nil {
		t.Fatalf("build wxexport: %v", buildErr)
	}
	return binPath
}

type buildFailure struct {
	err error
	out []byte
}

func (b *buildFailure) Error() string { return b.err.Error() + "\n" + string(b.out) }

const (
	wxid       = "wxid_e2e_abcd"
	contactA   = "wxid_friend"
	passphrase = "e2e-test-passphrase-32-bytes!!!!" // exactly 32 bytes
)

func shardTable(username string) string {
	sum := md5.Sum([]byte(username))
	return "Msg_" + hex.EncodeToString(sum[:])
}

func runCmd(t *testing.T, env []string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command(wxexportBinary(t), args...)
	cmd.Env = env
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

func baseEnv(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	return []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"PATH=" + os.Getenv("PATH"),
	}
}

// --- part 1: key mechanics against reserve-respecting synthetic encrypted databases ---

// randomPage builds one page-aligned, reserve-respecting "plaintext" image: content fills only
// [0:UsableSize), the trailing reserve is left zero, exactly mirroring a real SQLCipher
// database's own layout (see the package doc comment above).
func randomPage(n int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	b := make([]byte, n*sqlcipher.PageSize)
	for p := 0; p < n; p++ {
		pg := b[p*sqlcipher.PageSize : (p+1)*sqlcipher.PageSize]
		r.Read(pg[:sqlcipher.UsableSize])
	}
	copy(b, "SQLite format 3\x00")
	return b
}

func TestE2EKeySetCheckForget(t *testing.T) {
	root := t.TempDir()
	dbStorage := filepath.Join(root, "xwechat_files", wxid+"_e2e1", "db_storage")
	write := func(rel string, seed int64) {
		salt := make([]byte, sqlcipher.SaltSize)
		for i := range salt {
			salt[i] = byte(i + len(rel))
		}
		key, err := sqlcipher.DeriveKey([]byte(passphrase), salt)
		if err != nil {
			t.Fatal(err)
		}
		enc, err := sqlciphertest.EncryptDB(randomPage(2, seed), key, salt)
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dbStorage, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, enc, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("contact/contact.db", 1)
	write("session/session.db", 2)
	write("message/message_0.db", 3)
	dataDir := filepath.Join(root, "xwechat_files")
	env := baseEnv(t)

	out, _, err := runCmd(t, env, "--data-dir", dataDir, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}

	if _, errOut, err := runCmd(t, env, "--data-dir", dataDir, "key", "set", "--passphrase", hex.EncodeToString([]byte(passphrase))); err != nil {
		t.Fatalf("key set: %v\n%s", err, errOut)
	}

	wrong := make([]byte, 32)
	if _, _, err := runCmd(t, env, "--data-dir", dataDir, "key", "set", "--passphrase", hex.EncodeToString(wrong)); err == nil {
		t.Fatal("wrong passphrase should have been rejected")
	}

	out, _, err = runCmd(t, env, "--data-dir", dataDir, "key", "check")
	if err != nil {
		t.Fatalf("key check: %v\n%s", err, out)
	}
	if !contains(out, "3/3 databases keyed") {
		t.Errorf("key check output:\n%s", out)
	}

	if _, _, err := runCmd(t, env, "--data-dir", dataDir, "key", "forget"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCmd(t, env, "--data-dir", dataDir, "key", "check"); err == nil {
		t.Fatal("key check should fail after forget")
	}
}

// --- part 2: contacts/export against plain (already-decrypted) databases ---

func buildPlainAccount(t *testing.T) (dataDir string) {
	t.Helper()
	root := t.TempDir()
	dbStorage := filepath.Join(root, "xwechat_files", wxid+"_e2e1", "db_storage")

	open := func(rel string) *sql.DB {
		dst := filepath.Join(dbStorage, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", dst)
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	exec := func(db *sql.DB, q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}

	cdb := open("contact/contact.db")
	exec(cdb, `CREATE TABLE contact(username TEXT, local_type INTEGER, alias TEXT, remark TEXT, nick_name TEXT)`)
	exec(cdb, `INSERT INTO contact VALUES (?,1,'','Friend','Friend')`, contactA)
	cdb.Close()

	sdb := open("session/session.db")
	exec(sdb, `CREATE TABLE SessionTable(username TEXT PRIMARY KEY, last_timestamp INTEGER, summary TEXT)`)
	exec(sdb, `INSERT INTO SessionTable VALUES (?, 1700000500, 'hello there')`, contactA)
	sdb.Close()

	table := shardTable(contactA)
	mdb := open("message/message_0.db")
	exec(mdb, `CREATE TABLE Name2Id(user_name TEXT PRIMARY KEY, is_session INTEGER)`)
	exec(mdb, `INSERT INTO Name2Id(user_name) VALUES (?)`, contactA) // rowid 1
	exec(mdb, `INSERT INTO Name2Id(user_name) VALUES (?)`, wxid)     // rowid 2
	exec(mdb, `CREATE TABLE "`+table+`"(local_id INTEGER PRIMARY KEY, server_id INTEGER,
		local_type INTEGER, sort_seq INTEGER, real_sender_id INTEGER, create_time INTEGER,
		source TEXT, message_content TEXT, WCDB_CT_message_content INTEGER, WCDB_CT_source INTEGER)`)
	exec(mdb, `INSERT INTO "`+table+`" VALUES (1,100,1,10,1,1700000100,NULL,'hello from the e2e fixture',0,0)`)
	exec(mdb, `INSERT INTO "`+table+`" VALUES (2,101,1,20,2,1700000200,NULL,'hi back',0,0)`)
	mdb.Close()

	return filepath.Join(root, "xwechat_files")
}

func TestE2EContactsAndExport(t *testing.T) {
	dataDir := buildPlainAccount(t)
	env := baseEnv(t)

	out, errOut, err := runCmd(t, env, "--data-dir", dataDir, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v\nstdout:%s\nstderr:%s", err, out, errOut)
	}
	if !contains(out, "unencrypted") {
		t.Errorf("doctor should report the plain databases as unencrypted:\n%s", out)
	}

	out, errOut, err = runCmd(t, env, "--data-dir", dataDir, "contacts")
	if err != nil {
		t.Fatalf("contacts: %v\nstdout:%s\nstderr:%s", err, out, errOut)
	}
	if !contains(out, contactA) || !contains(out, "hello there") {
		t.Errorf("contacts output:\n%s", out)
	}

	outDir := t.TempDir()
	out, errOut, err = runCmd(t, env, "--data-dir", dataDir, "export", "--contact", contactA, "--format", "json,csv,sqlite", "--out", outDir)
	if err != nil {
		t.Fatalf("export: %v\nstdout:%s\nstderr:%s", err, out, errOut)
	}

	jsonPath := filepath.Join(outDir, "Friend.json")
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("missing %s (export stdout: %s)", jsonPath, out)
	}
	var doc struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(doc.Messages) != 2 {
		t.Fatalf("messages = %d, want 2: %v", len(doc.Messages), doc.Messages)
	}

	csvPath := filepath.Join(outDir, "Friend_messages.csv")
	cf, err := os.Open(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	defer cf.Close()
	records, err := csv.NewReader(cf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 { // header + 2 messages
		t.Fatalf("csv rows = %d, want 3: %v", len(records), records)
	}

	sqlitePath := filepath.Join(outDir, "Friend.sqlite")
	sdb, err := sql.Open("sqlite", sqlitePath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	var n int
	if err := sdb.QueryRow(`SELECT count(*) FROM messages`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("sqlite messages count = %d, err=%v", n, err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
