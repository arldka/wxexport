package snapshot

import (
	"bytes"
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arldka/wxexport/internal/keys"
	"github.com/arldka/wxexport/internal/sqlcipher"
	"github.com/arldka/wxexport/internal/sqlcipher/sqlciphertest"
)

func mk(t *testing.T, dir, rel string, seed byte, withWAL bool) (keys.DB, sqlcipher.Key, []byte) {
	t.Helper()
	salt := bytes.Repeat([]byte{seed}, 16)
	k, _ := sqlcipher.DeriveKey(bytes.Repeat([]byte{seed + 1}, 32), salt)
	plain := make([]byte, 3*sqlcipher.PageSize)
	rand.New(rand.NewSource(int64(seed))).Read(plain[:sqlcipher.UsableSize])
	copy(plain, "SQLite format 3\x00")
	enc, _ := sqlciphertest.EncryptDB(plain, k, salt)
	p := filepath.Join(dir, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, enc, 0o644)
	if withWAL {
		newPage := bytes.Repeat([]byte{seed}, sqlcipher.PageSize)
		clear(newPage[sqlcipher.UsableSize:])
		wal, _ := sqlciphertest.BuildWAL([]sqlciphertest.WALFrame{{Pgno: 2, Commit: 3, Plain: newPage}}, k, salt, sqlciphertest.WALOpts{})
		os.WriteFile(p+"-wal", wal, 0o644)
		copy(plain[sqlcipher.PageSize:], newPage)
	}
	return keys.DB{Rel: rel, Path: p}, k, plain
}

func TestTakeDecryptsWithWALAndCleansUp(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	src := t.TempDir()
	d1, k1, want1 := mk(t, src, "message/message_0.db", 1, true)
	d2, k2, want2 := mk(t, src, "contact/contact.db", 9, false)
	wd, err := NewWorkDir()
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(wd.Path); fi.Mode().Perm() != 0o700 {
		t.Errorf("workdir perm %v", fi.Mode().Perm())
	}
	var progress int
	s, err := Take(context.Background(), wd, []keys.DB{d1, d2},
		map[string]sqlcipher.Key{d1.Rel: k1, d2.Rel: k2}, Options{Progress: func(d, n int, _ string) { progress = d }})
	if err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string][]byte{d1.Rel: want1, d2.Rel: want2} {
		got, _ := os.ReadFile(s.Path(rel))
		if !bytes.Equal(got, want) {
			t.Errorf("%s: decrypted content differs", rel)
		}
		if fi, _ := os.Stat(s.Path(rel)); fi.Mode().Perm() != 0o600 {
			t.Errorf("%s perm %v", rel, fi.Mode().Perm())
		}
	}
	if s.Stats[d1.Rel].WALFrames != 1 || progress != 2 {
		t.Errorf("stats %+v progress %d", s.Stats[d1.Rel], progress)
	}
	if _, err := os.Stat(filepath.Join(wd.Path, "raw", d1.Rel)); !os.IsNotExist(err) {
		t.Error("raw copy must be removed")
	}
	// the live source files must be untouched
	if b, _ := os.ReadFile(d1.Path); len(b) != 3*sqlcipher.PageSize {
		t.Error("source modified")
	}
	if err := wd.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wd.Path); !os.IsNotExist(err) {
		t.Error("workdir not removed")
	}
}

func TestTakeMissingKeyAndPlain(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	src := t.TempDir()
	d, _, _ := mk(t, src, "a/a.db", 1, false)
	wd, _ := NewWorkDir()
	defer wd.Cleanup()
	if _, err := Take(context.Background(), wd, []keys.DB{d}, nil, Options{}); err == nil {
		t.Fatal("encrypted db without key must fail")
	}
	pp := filepath.Join(src, "plain.db")
	b := make([]byte, sqlcipher.PageSize)
	copy(b, "SQLite format 3\x00")
	os.WriteFile(pp, b, 0o644)
	s, err := Take(context.Background(), wd, []keys.DB{{Rel: "plain.db", Path: pp}}, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(s.Path("plain.db")); !bytes.Equal(got, b) {
		t.Error("plain db not copied")
	}
}

type fakeInfo struct {
	os.FileInfo
	size int64
	mt   time.Time
}

func (f fakeInfo) Size() int64        { return f.size }
func (f fakeInfo) ModTime() time.Time { return f.mt }

func TestCopyConsistentRetries(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "s.db")
	os.WriteFile(src, []byte("hello"), 0o644)
	calls := 0
	stat := func(string) (os.FileInfo, error) {
		calls++
		// calls 1,2 (attempt 1): changed; calls 3,4 (attempt 2): stable
		mt := time.Unix(100, 0)
		if calls == 2 {
			mt = time.Unix(200, 0)
		}
		return fakeInfo{size: 5, mt: mt}, nil
	}
	if err := copyConsistent(src, filepath.Join(dir, "d.db"), stat); err != nil || calls != 4 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	n := 0
	always := func(string) (os.FileInfo, error) {
		n++
		return fakeInfo{size: int64(n), mt: time.Unix(int64(n), 0)}, nil
	}
	if err := copyConsistent(src, filepath.Join(dir, "d2.db"), always); err == nil {
		t.Fatal("should give up after max tries")
	}
}

func TestTakeCancelled(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	src := t.TempDir()
	d, k, _ := mk(t, src, "a.db", 1, false)
	wd, _ := NewWorkDir()
	defer wd.Cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Take(ctx, wd, []keys.DB{d}, map[string]sqlcipher.Key{d.Rel: k}, Options{}); err == nil {
		t.Fatal("expected cancellation error")
	}
}
