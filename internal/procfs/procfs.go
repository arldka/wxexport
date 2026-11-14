// Package procfs reads the bits of /proc we need (root-injectable for tests).
package procfs

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FS is a /proc mount.
type FS struct{ Root string }

func Default() FS { return FS{Root: "/proc"} }

type Map struct {
	Start, End uint64
	Perms      string
	Offset     uint64
	Path       string
}

func (f FS) p(pid int, rest ...string) string {
	return filepath.Join(append([]string{f.Root, strconv.Itoa(pid)}, rest...)...)
}

// Pids lists numeric entries of the proc root.
func (f FS) Pids() []int {
	ents, _ := os.ReadDir(f.Root)
	var out []int
	for _, e := range ents {
		if n, err := strconv.Atoi(e.Name()); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func (f FS) Exe(pid int) (string, error) { return os.Readlink(f.p(pid, "exe")) }

// PPid reads the parent pid from status.
func (f FS) PPid(pid int) (int, error) {
	fh, err := os.Open(f.p(pid, "status"))
	if err != nil {
		return 0, err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "PPid:"); ok {
			return strconv.Atoi(strings.TrimSpace(v))
		}
	}
	return 0, fmt.Errorf("procfs: no PPid for %d", pid)
}

// Tasks lists thread ids of a process.
func (f FS) Tasks(pid int) []int {
	ents, _ := os.ReadDir(f.p(pid, "task"))
	var out []int
	for _, e := range ents {
		if n, err := strconv.Atoi(e.Name()); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// Descends reports whether pid has ancestor somewhere in its PPid chain (or equals it).
func (f FS) Descends(pid, ancestor int) bool {
	for i := 0; i < 64 && pid > 1; i++ {
		if pid == ancestor {
			return true
		}
		pp, err := f.PPid(pid)
		if err != nil {
			return false
		}
		pid = pp
	}
	return pid == ancestor
}

// FindByExe returns pids whose exe link equals exe exactly.
func (f FS) FindByExe(exe string) []int {
	var out []int
	for _, pid := range f.Pids() {
		if got, err := f.Exe(pid); err == nil && got == exe {
			out = append(out, pid)
		}
	}
	return out
}

// Maps parses /proc/<pid>/maps.
func (f FS) Maps(pid int) ([]Map, error) {
	fh, err := os.Open(f.p(pid, "maps"))
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	return ParseMaps(bufio.NewScanner(fh))
}

func ParseMaps(sc *bufio.Scanner) ([]Map, error) {
	var out []Map
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 {
			continue
		}
		lo, hi, ok := strings.Cut(fields[0], "-")
		if !ok {
			continue
		}
		s, err1 := strconv.ParseUint(lo, 16, 64)
		e, err2 := strconv.ParseUint(hi, 16, 64)
		off, err3 := strconv.ParseUint(fields[2], 16, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, fmt.Errorf("procfs: bad maps line %q", sc.Text())
		}
		m := Map{Start: s, End: e, Perms: fields[1], Offset: off}
		if len(fields) >= 6 {
			m.Path = strings.Join(fields[5:], " ")
		}
		out = append(out, m)
	}
	return out, sc.Err()
}

// ImageBase returns the load base of a mapped file: the start of the offset-0 mapping for path.
func ImageBase(maps []Map, path string) (uint64, bool) {
	for _, m := range maps {
		if m.Path == path && m.Offset == 0 {
			return m.Start, true
		}
	}
	return 0, false
}

// PtraceScope reads kernel.yama.ptrace_scope (-1 if unavailable).
func PtraceScope() int {
	b, err := os.ReadFile("/proc/sys/kernel/yama/ptrace_scope")
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return -1
	}
	return n
}
