// Package discover finds the WeChat flatpak install, account folders and database files.
package discover

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/arldka/wxexport/internal/keys"
	"github.com/arldka/wxexport/internal/procfs"
)

const (
	FlatpakID = "com.tencent.WeChat"
	// SandboxBinary is the path of the WeChat executable as seen from inside the sandbox
	// (what /proc/<pid>/exe and maps report).
	SandboxBinary = "/app/extra/wechat/wechat"
)

type Runner func(name string, args ...string) ([]byte, error)

func ExecRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

type Install struct {
	Location   string // flatpak deployment dir
	BinaryPath string // host path of the wechat ELF
	Version    string
}

// FindInstall queries flatpak. The binary lives under <location>/files/extra/wechat/wechat.
func FindInstall(run Runner) (Install, error) {
	loc, err := run("flatpak", "info", "--show-location", FlatpakID)
	if err != nil {
		return Install{}, fmt.Errorf("discover: flatpak app %s not found: %w", FlatpakID, err)
	}
	in := Install{Location: strings.TrimSpace(string(loc))}
	in.BinaryPath = filepath.Join(in.Location, "files", "extra", "wechat", "wechat")
	if info, err := run("flatpak", "info", FlatpakID); err == nil {
		for _, l := range strings.Split(string(info), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(l), "Version:"); ok {
				in.Version = strings.TrimSpace(v)
			}
		}
	}
	if _, err := os.Stat(in.BinaryPath); err != nil {
		return in, fmt.Errorf("discover: wechat binary missing at %s: %w", in.BinaryPath, err)
	}
	return in, nil
}

// DataRoots returns candidate xwechat_files directories that exist.
func DataRoots(home string) []string {
	cands := []string{
		filepath.Join(home, ".var/app", FlatpakID, "xwechat_files"),
		filepath.Join(home, "Documents/xwechat_files"),
		filepath.Join(home, "xwechat_files"),
	}
	var out []string
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			out = append(out, c)
		}
	}
	return out
}

type Account struct {
	Dir       string // .../xwechat_files/<name>
	Name      string // folder name, e.g. wxid_abc_e409
	Wxid      string // own wxid, e.g. wxid_abc
	DBStorage string
}

var suffixRe = regexp.MustCompile(`^(.+)_[0-9a-f]{4}$`)

// OwnWxid strips the 4-hex-digit suffix WeChat 4 appends to the account folder.
func OwnWxid(folder string) string {
	if m := suffixRe.FindStringSubmatch(folder); m != nil {
		return m[1]
	}
	return folder
}

// Accounts lists account folders (those containing db_storage) under root, newest db first.
func Accounts(root string) ([]Account, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	type acc struct {
		a Account
		m int64
	}
	var list []acc
	for _, e := range ents {
		if !e.IsDir() || e.Name() == "all_users" {
			continue
		}
		dbs := filepath.Join(root, e.Name(), "db_storage")
		fi, err := os.Stat(dbs)
		if err != nil || !fi.IsDir() {
			continue
		}
		list = append(list, acc{Account{Dir: filepath.Join(root, e.Name()), Name: e.Name(), Wxid: OwnWxid(e.Name()), DBStorage: dbs}, fi.ModTime().UnixNano()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].m > list[j].m })
	out := make([]Account, len(list))
	for i := range list {
		out[i] = list[i].a
	}
	if len(out) == 0 {
		return nil, errors.New("discover: no account with db_storage under " + root)
	}
	return out, nil
}

// Inventory lists every *.db file under db_storage/<module>/ (excluding -wal/-shm/material files).
func Inventory(a Account) ([]keys.DB, error) {
	paths, err := filepath.Glob(filepath.Join(a.DBStorage, "*", "*.db"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	out := make([]keys.DB, 0, len(paths))
	for _, p := range paths {
		rel, _ := filepath.Rel(a.DBStorage, p)
		out = append(out, keys.DB{Rel: filepath.ToSlash(rel), Path: p})
	}
	return out, nil
}

var shardRe = regexp.MustCompile(`^message/message_\d+\.db$`)

// IsShard reports whether rel is a message shard (message_N.db).
func IsShard(rel string) bool { return shardRe.MatchString(rel) }

// CoreDBs keeps only the databases needed for export: contact, session and message shards.
func CoreDBs(all []keys.DB) []keys.DB {
	var out []keys.DB
	for _, d := range all {
		if d.Rel == "contact/contact.db" || d.Rel == "session/session.db" || IsShard(d.Rel) {
			out = append(out, d)
		}
	}
	return out
}

// RunningPids returns pids of the sandboxed WeChat main executable.
func RunningPids(fs procfs.FS) []int { return fs.FindByExe(SandboxBinary) }
