package keys_test

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arldka/wxexport/internal/keys"
	"github.com/arldka/wxexport/internal/sqlcipher"
	"github.com/arldka/wxexport/internal/sqlcipher/sqlciphertest"
)

var pass = func() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(100 + i)
	}
	return b
}()

// mkDB writes an encrypted 2-page db with the given salt seed and returns its keys.DB.
func mkDB(t *testing.T, dir, rel string, seed byte, p []byte) keys.DB {
	t.Helper()
	salt := make([]byte, 16)
	for i := range salt {
		salt[i] = seed + byte(i)
	}
	k, err := sqlcipher.DeriveKey(p, salt)
	if err != nil {
		t.Fatal(err)
	}
	plain := make([]byte, 2*sqlcipher.PageSize)
	copy(plain, "SQLite format 3\x00")
	enc, err := sqlciphertest.EncryptDB(plain, k, salt)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, rel)
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path, enc, 0o600); err != nil {
		t.Fatal(err)
	}
	return keys.DB{Rel: rel, Path: path}
}

func TestSaveLoadPerms(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cfg", "keys.json")
	c := &keys.Cache{Account: "wxid_x", PassphraseHex: hex.EncodeToString(pass)}
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	di, _ := os.Stat(filepath.Dir(p))
	if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
		t.Errorf("perms file=%v dir=%v", fi.Mode().Perm(), di.Mode().Perm())
	}
	got, err := keys.Load(p)
	if err != nil || got.Account != "wxid_x" || string(got.Passphrase()) != string(pass) {
		t.Fatalf("%+v %v", got, err)
	}
	if none, err := keys.Load(filepath.Join(t.TempDir(), "x")); none != nil || err != nil {
		t.Errorf("missing file => nil,nil; got %v %v", none, err)
	}
}

func TestResolveChain(t *testing.T) {
	dir := t.TempDir()
	a := mkDB(t, dir, "message/message_0.db", 1, pass)
	b := mkDB(t, dir, "contact/contact.db", 50, pass)
	c := &keys.Cache{PassphraseHex: hex.EncodeToString(pass)}

	// First pass: nothing cached, so both derive from the passphrase.
	ks, res, err := keys.Resolve(c, []keys.DB{a, b})
	if err != nil || len(ks) != 2 {
		t.Fatalf("%v %v", err, res)
	}
	for _, r := range res {
		if r.Status != keys.StatusDerived {
			t.Errorf("%s: %s", r.DB.Rel, r.Status)
		}
	}
	if len(c.DBs) != 2 {
		t.Fatal("cache not updated")
	}

	// Second pass: cached enc keys are used; passphrase no longer needed.
	c.PassphraseHex = ""
	_, res, err = keys.Resolve(c, []keys.DB{a, b})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Status != keys.StatusCached {
			t.Errorf("%s: %s", r.DB.Rel, r.Status)
		}
	}
}

func TestResolveNewSaltFallsBackToPassphrase(t *testing.T) {
	dir := t.TempDir()
	a := mkDB(t, dir, "x.db", 1, pass)
	c := &keys.Cache{PassphraseHex: hex.EncodeToString(pass)}
	if _, _, err := keys.Resolve(c, []keys.DB{a}); err != nil {
		t.Fatal(err)
	}
	a = mkDB(t, dir, "x.db", 77, pass) // db recreated with a new salt
	_, res, err := keys.Resolve(c, []keys.DB{a})
	if err != nil || res[0].Status != keys.StatusDerived {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestResolveNoKey(t *testing.T) {
	dir := t.TempDir()
	a := mkDB(t, dir, "x.db", 1, pass)
	_, res, err := keys.Resolve(&keys.Cache{}, []keys.DB{a})
	if !errors.Is(err, keys.ErrNoKey) || res[0].Status != keys.StatusMissing {
		t.Fatalf("%v %+v", err, res)
	}
	// wrong passphrase must also fail, and the error must not leak the passphrase
	wrong := hex.EncodeToString(make([]byte, 32))
	_, _, err = keys.Resolve(&keys.Cache{PassphraseHex: wrong}, []keys.DB{a})
	if !errors.Is(err, keys.ErrNoKey) || strings.Contains(err.Error(), wrong) {
		t.Fatalf("%v", err)
	}
}

func TestResolvePlainAndMissingFile(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "p.db")
	b := make([]byte, sqlcipher.PageSize)
	copy(b, "SQLite format 3\x00")
	os.WriteFile(plain, b, 0o600)
	_, res, err := keys.Resolve(&keys.Cache{}, []keys.DB{{Rel: "p.db", Path: plain}, {Rel: "gone.db", Path: filepath.Join(dir, "gone.db")}})
	if err != nil {
		t.Fatalf("plain db should not make Resolve fail: %v", err)
	}
	if res[0].Status != keys.StatusPlain || res[1].Status != keys.StatusError {
		t.Errorf("%+v", res)
	}
}

func TestTryPassphrase(t *testing.T) {
	dir := t.TempDir()
	d := mkDB(t, dir, "x.db", 3, pass)
	page1, _ := os.ReadFile(d.Path)
	if _, ok := keys.TryPassphrase(pass, page1[:4096]); !ok {
		t.Error("right passphrase rejected")
	}
	if _, ok := keys.TryPassphrase(make([]byte, 32), page1[:4096]); ok {
		t.Error("wrong passphrase accepted")
	}
}
