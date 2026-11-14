package sqlcipher_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/arldka/wxexport/internal/sqlcipher"
	"github.com/arldka/wxexport/internal/sqlcipher/sqlciphertest"
)

func testKey(t *testing.T) (sqlcipher.Key, []byte) {
	t.Helper()
	pass := make([]byte, 32)
	salt := make([]byte, 16)
	for i := range pass {
		pass[i] = byte(i)
	}
	for i := range salt {
		salt[i] = byte(16 + i)
	}
	k, err := sqlcipher.DeriveKey(pass, salt)
	if err != nil {
		t.Fatal(err)
	}
	return k, salt
}

// plainImage returns n random pages (reserve zeroed) as the decrypted form would look.
func plainImage(n int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	b := make([]byte, n*sqlcipher.PageSize)
	for p := 0; p < n; p++ {
		pg := b[p*sqlcipher.PageSize : (p+1)*sqlcipher.PageSize]
		r.Read(pg[:sqlcipher.UsableSize])
	}
	copy(b, "SQLite format 3\x00")
	return b
}

func TestDeriveKeyKnownAnswer(t *testing.T) {
	k, _ := testKey(t) // vectors generated independently with python hashlib.pbkdf2_hmac
	if got := hex.EncodeToString(k.Enc[:]); got != "699f2ac9232d8ea2f443137cb626202ca98943df031715298614a5251ee7d973" {
		t.Errorf("enc key = %s", got)
	}
	if got := hex.EncodeToString(k.Mac[:]); got != "729f34606388116b966ffd5af83bc4a95b0768d83b69b87e98a21c695e9c4880" {
		t.Errorf("mac key = %s", got)
	}
}

func TestKeyRedacted(t *testing.T) {
	k, _ := testKey(t)
	for _, s := range []string{k.String(), (&k).String()} {
		if bytes.Contains([]byte(s), []byte("69")) && len(s) > 30 {
			t.Errorf("unexpected: %s", s)
		}
	}
}

func writeFile(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRoundTrip(t *testing.T) {
	k, salt := testKey(t)
	plain := plainImage(5, 1)
	enc, err := sqlciphertest.EncryptDB(plain, k, salt)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := writeFile(t, dir, "a.db", enc)
	dst := filepath.Join(dir, "a.plain")
	st, err := sqlcipher.DecryptFile(src, "", dst, k)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got, plain) {
		t.Fatal("decrypted image differs")
	}
	if st.Pages != 5 || st.FinalPages != 5 {
		t.Errorf("stats %+v", st)
	}
	if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0o600 {
		t.Errorf("dst perm %v", fi.Mode().Perm())
	}
	if s, err := sqlcipher.ReadSalt(src); err != nil || !bytes.Equal(s, salt) {
		t.Errorf("ReadSalt = %x %v", s, err)
	}
}

func TestWrongKey(t *testing.T) {
	k, salt := testKey(t)
	enc, _ := sqlciphertest.EncryptDB(plainImage(2, 2), k, salt)
	dir := t.TempDir()
	src := writeFile(t, dir, "a.db", enc)
	other, _ := sqlcipher.DeriveKey(bytes.Repeat([]byte{9}, 32), salt)
	_, err := sqlcipher.DecryptFile(src, "", filepath.Join(dir, "o"), other)
	if !errors.Is(err, sqlcipher.ErrBadKey) {
		t.Fatalf("want ErrBadKey, got %v", err)
	}
}

func TestCorruptPageNamed(t *testing.T) {
	k, salt := testKey(t)
	enc, _ := sqlciphertest.EncryptDB(plainImage(4, 3), k, salt)
	enc[2*sqlcipher.PageSize+100] ^= 0xff // page 3
	dir := t.TempDir()
	src := writeFile(t, dir, "a.db", enc)
	_, err := sqlcipher.DecryptFile(src, "", filepath.Join(dir, "o"), k)
	var pe *sqlcipher.PageError
	if !errors.As(err, &pe) || pe.Page != 3 || pe.WAL {
		t.Fatalf("want PageError page 3, got %v", err)
	}
}

func TestBadSize(t *testing.T) {
	k, _ := testKey(t)
	dir := t.TempDir()
	src := writeFile(t, dir, "a.db", make([]byte, 5000))
	if _, err := sqlcipher.DecryptFile(src, "", filepath.Join(dir, "o"), k); err == nil {
		t.Fatal("expected size error")
	}
}

func page(seed int64) []byte { return plainImage(1, seed) }

func TestWALCommittedOnly(t *testing.T) {
	for _, big := range []bool{false, true} {
		k, salt := testKey(t)
		base := plainImage(3, 10)
		enc, _ := sqlciphertest.EncryptDB(base, k, salt)
		p2a, p2b, p4, p3 := page(21), page(22), page(24), page(23)
		frames := []sqlciphertest.WALFrame{
			{Pgno: 2, Plain: p2a},           // overwritten later in same tx
			{Pgno: 4, Commit: 4, Plain: p4}, // commit: db grows to 4
			{Pgno: 2, Plain: p2b},           // uncommitted tail below
			{Pgno: 3, Plain: p3},            // never committed
		}
		// Make frame 2,3 "uncommitted": no commit frame follows them.
		frames[0].Plain = p2a
		wal, err := sqlciphertest.BuildWAL(frames, k, salt, sqlciphertest.WALOpts{BigEndian: big})
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		src := writeFile(t, dir, "a.db", enc)
		walp := writeFile(t, dir, "a.db-wal", wal)
		dst := filepath.Join(dir, "o")
		st, err := sqlcipher.DecryptFile(src, walp, dst, k)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(dst)
		if len(got) != 4*sqlcipher.PageSize {
			t.Fatalf("big=%v size %d, want 4 pages", big, len(got))
		}
		pg := func(n int) []byte { return got[(n-1)*sqlcipher.PageSize : n*sqlcipher.PageSize] }
		if !bytes.Equal(pg(2), p2a) {
			t.Errorf("big=%v page2 should be the committed version", big)
		}
		if !bytes.Equal(pg(4), p4) {
			t.Errorf("big=%v page4 mismatch", big)
		}
		if !bytes.Equal(pg(3), base[2*sqlcipher.PageSize:3*sqlcipher.PageSize]) {
			t.Errorf("big=%v uncommitted page3 must not be applied", big)
		}
		if st.WALFrames != 2 || st.WALPagesApplied != 2 {
			t.Errorf("stats %+v", st)
		}
	}
}

func TestWALLaterFrameWins(t *testing.T) {
	k, salt := testKey(t)
	enc, _ := sqlciphertest.EncryptDB(plainImage(2, 11), k, salt)
	a, b := page(31), page(32)
	wal, _ := sqlciphertest.BuildWAL([]sqlciphertest.WALFrame{
		{Pgno: 2, Commit: 2, Plain: a},
		{Pgno: 2, Commit: 2, Plain: b},
	}, k, salt, sqlciphertest.WALOpts{})
	dir := t.TempDir()
	src := writeFile(t, dir, "a.db", enc)
	walp := writeFile(t, dir, "a.db-wal", wal)
	dst := filepath.Join(dir, "o")
	if _, err := sqlcipher.DecryptFile(src, walp, dst, k); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got[sqlcipher.PageSize:], b) {
		t.Error("expected last frame to win")
	}
}

func TestWALShrinks(t *testing.T) {
	k, salt := testKey(t)
	enc, _ := sqlciphertest.EncryptDB(plainImage(4, 12), k, salt)
	wal, _ := sqlciphertest.BuildWAL([]sqlciphertest.WALFrame{{Pgno: 1, Commit: 2, Plain: page(41)}}, k, salt, sqlciphertest.WALOpts{})
	dir := t.TempDir()
	src := writeFile(t, dir, "a.db", enc)
	walp := writeFile(t, dir, "a.db-wal", wal)
	dst := filepath.Join(dir, "o")
	st, err := sqlcipher.DecryptFile(src, walp, dst, k)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dst); fi.Size() != 2*sqlcipher.PageSize || st.FinalPages != 2 {
		t.Errorf("size %d stats %+v", fi.Size(), st)
	}
}

func TestWALStopsAtInvalidFrame(t *testing.T) {
	cases := map[string]sqlciphertest.WALOpts{
		"salt mismatch": {FrameSalt: map[int][2]uint32{1: {9, 9}}},
		"bad checksum":  {CorruptChecksum: map[int]bool{1: true}},
	}
	for name, o := range cases {
		k, salt := testKey(t)
		base := plainImage(3, 13)
		enc, _ := sqlciphertest.EncryptDB(base, k, salt)
		first, second := page(51), page(52)
		wal, _ := sqlciphertest.BuildWAL([]sqlciphertest.WALFrame{
			{Pgno: 2, Commit: 3, Plain: first},
			{Pgno: 3, Commit: 3, Plain: second}, // invalid frame: must be ignored
		}, k, salt, o)
		dir := t.TempDir()
		src := writeFile(t, dir, "a.db", enc)
		walp := writeFile(t, dir, "a.db-wal", wal)
		dst := filepath.Join(dir, "o")
		if _, err := sqlcipher.DecryptFile(src, walp, dst, k); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, _ := os.ReadFile(dst)
		if !bytes.Equal(got[sqlcipher.PageSize:2*sqlcipher.PageSize], first) {
			t.Errorf("%s: first frame should apply", name)
		}
		if !bytes.Equal(got[2*sqlcipher.PageSize:], base[2*sqlcipher.PageSize:]) {
			t.Errorf("%s: invalid frame must not apply", name)
		}
	}
}

func TestWALBadHeaderIgnoredAndTruncated(t *testing.T) {
	r, err := sqlcipher.ParseWAL([]byte("garbage that is long enough to look like a header....."))
	if err != nil || len(r.Pages) != 0 {
		t.Fatalf("bad magic should yield empty result: %v %v", r, err)
	}
	k, salt := testKey(t)
	wal, _ := sqlciphertest.BuildWAL([]sqlciphertest.WALFrame{{Pgno: 1, Commit: 1, Plain: page(61)}}, k, salt, sqlciphertest.WALOpts{})
	r, _ = sqlcipher.ParseWAL(wal[:len(wal)-10]) // truncated frame
	if len(r.Pages) != 0 {
		t.Fatal("truncated frame must be ignored")
	}
}

func TestWALCorruptEncryptedFrameIsHardError(t *testing.T) {
	k, salt := testKey(t)
	enc, _ := sqlciphertest.EncryptDB(plainImage(2, 14), k, salt)
	other, _ := sqlcipher.DeriveKey(bytes.Repeat([]byte{7}, 32), salt)
	// Valid WAL structure/checksums but frame encrypted under another key.
	wal, _ := sqlciphertest.BuildWAL([]sqlciphertest.WALFrame{{Pgno: 2, Commit: 2, Plain: page(71)}}, other, salt, sqlciphertest.WALOpts{})
	dir := t.TempDir()
	src := writeFile(t, dir, "a.db", enc)
	walp := writeFile(t, dir, "a.db-wal", wal)
	_, err := sqlcipher.DecryptFile(src, walp, filepath.Join(dir, "o"), k)
	var pe *sqlcipher.PageError
	if !errors.As(err, &pe) || !pe.WAL || pe.Page != 2 {
		t.Fatalf("want WAL PageError page 2, got %v", err)
	}
}

func TestZeroPageAllowed(t *testing.T) {
	k, salt := testKey(t)
	enc, _ := sqlciphertest.EncryptDB(plainImage(3, 15), k, salt)
	clear(enc[sqlcipher.PageSize : 2*sqlcipher.PageSize]) // page 2 never written
	dir := t.TempDir()
	src := writeFile(t, dir, "a.db", enc)
	dst := filepath.Join(dir, "o")
	if _, err := sqlcipher.DecryptFile(src, "", dst, k); err != nil {
		t.Fatal(err)
	}
}
