package procfs

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func fake(t *testing.T, procs map[int]struct {
	ppid int
	exe  string
}) FS {
	root := t.TempDir()
	for pid, p := range procs {
		d := filepath.Join(root, strconv.Itoa(pid))
		os.MkdirAll(filepath.Join(d, "task", strconv.Itoa(pid)), 0o755)
		os.WriteFile(filepath.Join(d, "status"), []byte("Name:\tx\nPPid:\t"+strconv.Itoa(p.ppid)+"\nTracerPid:\t0\n"), 0o644)
		os.Symlink(p.exe, filepath.Join(d, "exe"))
	}
	os.MkdirAll(filepath.Join(root, "self"), 0o755) // non-numeric ignored
	return FS{Root: root}
}

func TestDescendsAndFind(t *testing.T) {
	type pr = struct {
		ppid int
		exe  string
	}
	fs := fake(t, map[int]pr{
		10: {1, "/usr/bin/flatpak"},
		20: {10, "/usr/bin/bwrap"},
		30: {20, "/app/extra/wechat/wechat"},
		40: {1, "/app/extra/wechat/wechat"}, // unrelated instance
	})
	if !fs.Descends(30, 10) || fs.Descends(40, 10) || !fs.Descends(10, 10) {
		t.Error("Descends wrong")
	}
	got := fs.FindByExe("/app/extra/wechat/wechat")
	if len(got) != 2 {
		t.Errorf("FindByExe = %v", got)
	}
	if ts := fs.Tasks(30); len(ts) != 1 || ts[0] != 30 {
		t.Errorf("Tasks = %v", ts)
	}
	if len(fs.Pids()) != 4 {
		t.Errorf("Pids = %v", fs.Pids())
	}
}

func TestParseMapsAndBase(t *testing.T) {
	in := `55d4a1c00000-55d4a2a00000 r--p 00000000 00:2a 123 /app/extra/wechat/wechat
55d4a2a00000-55d4a8000000 r-xp 0e400000 00:2a 123 /app/extra/wechat/wechat
7ffc1000-7ffc2000 rw-p 00000000 00:00 0 [stack]
7f00-7f10 r--p 00000000 00:00 0 /path with space/lib.so
`
	maps, err := ParseMaps(bufio.NewScanner(strings.NewReader(in)))
	if err != nil || len(maps) != 4 {
		t.Fatalf("%v %v", maps, err)
	}
	if b, ok := ImageBase(maps, "/app/extra/wechat/wechat"); !ok || b != 0x55d4a1c00000 {
		t.Errorf("base %x %v", b, ok)
	}
	if maps[1].Perms != "r-xp" || maps[1].Offset != 0x0e400000 || maps[3].Path != "/path with space/lib.so" {
		t.Errorf("%+v", maps)
	}
	if _, err := ParseMaps(bufio.NewScanner(strings.NewReader("zz-yy r--p 0 0 0 x\n"))); err == nil {
		t.Error("bad line should error")
	}
}
