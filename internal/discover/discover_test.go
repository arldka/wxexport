package discover

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOwnWxid(t *testing.T) {
	for in, want := range map[string]string{
		"wxid_abc123def456_a1b2": "wxid_abc123def456",
		"alice_01ab":               "alice",
		"wxid_abc":                 "wxid_abc",  // no 4-hex suffix
		"wxid_zzzz":                "wxid_zzzz", // zzzz is not hex
	} {
		if got := OwnWxid(in); got != want {
			t.Errorf("OwnWxid(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFindInstall(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "files/extra/wechat")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "wechat"), []byte("x"), 0o755)
	run := func(name string, args ...string) ([]byte, error) {
		if len(args) == 3 {
			return []byte(dir + "\n"), nil
		}
		return []byte("ID: com.tencent.WeChat\n    Version: 4.1.13.26\n"), nil
	}
	in, err := FindInstall(run)
	if err != nil || in.Version != "4.1.13.26" || in.BinaryPath != filepath.Join(bin, "wechat") {
		t.Fatalf("%+v %v", in, err)
	}
	if _, err := FindInstall(func(string, ...string) ([]byte, error) { return nil, errors.New("nope") }); err == nil {
		t.Error("expected error when flatpak app is missing")
	}
	dir2 := t.TempDir()
	if _, err := FindInstall(func(string, ...string) ([]byte, error) { return []byte(dir2), nil }); err == nil {
		t.Error("expected error when binary is missing")
	}
}

func TestAccountsAndInventory(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".var/app/com.tencent.WeChat/xwechat_files")
	mk := func(p string) { os.MkdirAll(filepath.Join(root, p), 0o755) }
	touch := func(p string) { os.WriteFile(filepath.Join(root, p), []byte("x"), 0o600) }
	mk("all_users")
	mk("wxid_me_abcd/db_storage/message")
	mk("wxid_me_abcd/db_storage/contact")
	mk("wxid_me_abcd/db_storage/session")
	mk("stray/other")
	for _, f := range []string{"message/message_0.db", "message/message_1.db", "message/message_fts.db", "message/message_resource.db",
		"message/message_0.db-wal", "message/message_0.db-first.material", "contact/contact.db", "session/session.db"} {
		touch("wxid_me_abcd/db_storage/" + f)
	}
	if r := DataRoots(home); len(r) != 1 || r[0] != root {
		t.Fatalf("DataRoots %v", r)
	}
	accs, err := Accounts(root)
	if err != nil || len(accs) != 1 || accs[0].Wxid != "wxid_me" {
		t.Fatalf("%+v %v", accs, err)
	}
	inv, _ := Inventory(accs[0])
	if len(inv) != 6 { // two shards, fts, resource, contact, session
		t.Errorf("inventory %v", inv)
	}
	core := CoreDBs(inv)
	if len(core) != 4 {
		t.Errorf("core %v", core)
	}
	for _, d := range core {
		if d.Rel == "message/message_fts.db" || d.Rel == "message/message_resource.db" {
			t.Errorf("non-core db %s", d.Rel)
		}
	}
	if _, err := Accounts(t.TempDir()); err == nil {
		t.Error("empty root should error")
	}
}
