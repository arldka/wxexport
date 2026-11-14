// Package snapshot copies live WeChat databases (+WAL) read-only into a private work
// directory and decrypts them to plain SQLite files. SQLite only ever opens the copies.
package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"

	"github.com/arldka/wxexport/internal/config"
	"github.com/arldka/wxexport/internal/keys"
	"github.com/arldka/wxexport/internal/sqlcipher"
)

const maxCopyTries = 5

// WorkDir is a 0700 directory removed by Cleanup.
type WorkDir struct{ Path string }

// NewWorkDir creates $XDG_RUNTIME_DIR/wxexport-<pid> (0700).
func NewWorkDir() (*WorkDir, error) {
	p := filepath.Join(config.RuntimeDir(), "wxexport-"+strconv.Itoa(os.Getpid()))
	if err := os.RemoveAll(p); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(p, 0o700); err != nil {
		return nil, err
	}
	return &WorkDir{Path: p}, nil
}

func (w *WorkDir) Cleanup() error { return os.RemoveAll(w.Path) }

type statFn func(string) (os.FileInfo, error)

// Options tune a snapshot; zero value is fine.
type Options struct {
	Stat     statFn // injectable for tests
	Progress func(done, total int, rel string)
}

type Snapshot struct {
	Dir   string                     // root of plain copies
	Files map[string]string          // rel -> plain sqlite path
	Stats map[string]sqlcipher.Stats // rel -> decrypt stats
}

// Path returns the plain copy of rel.
func (s *Snapshot) Path(rel string) string { return s.Files[rel] }

// Take snapshots dbs into wd. ks maps rel -> key; databases without a key are skipped
// only if they are already plain SQLite (copied as is), otherwise it is an error.
func Take(ctx context.Context, wd *WorkDir, dbs []keys.DB, ks map[string]sqlcipher.Key, opt Options) (*Snapshot, error) {
	if opt.Stat == nil {
		opt.Stat = os.Stat
	}
	s := &Snapshot{Dir: filepath.Join(wd.Path, "plain"), Files: map[string]string{}, Stats: map[string]sqlcipher.Stats{}}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return nil, err
	}
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		errs []error
		done int
	)
	sem := make(chan struct{}, max(2, runtime.NumCPU()/2))
	for _, d := range dbs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			st, out, err := one(wd, d, ks, opt)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", d.Rel, err))
				return
			}
			s.Files[d.Rel] = out
			s.Stats[d.Rel] = st
			done++
			if opt.Progress != nil {
				opt.Progress(done, len(dbs), d.Rel)
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return s, nil
}

func one(wd *WorkDir, d keys.DB, ks map[string]sqlcipher.Key, opt Options) (sqlcipher.Stats, string, error) {
	raw := filepath.Join(wd.Path, "raw", d.Rel)
	if err := os.MkdirAll(filepath.Dir(raw), 0o700); err != nil {
		return sqlcipher.Stats{}, "", err
	}
	defer os.Remove(raw)
	defer os.Remove(raw + "-wal")
	if err := copyConsistent(d.Path, raw, opt.Stat); err != nil {
		return sqlcipher.Stats{}, "", err
	}
	out := filepath.Join(wd.Path, "plain", d.Rel)
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return sqlcipher.Stats{}, "", err
	}
	key, ok := ks[d.Rel]
	if !ok {
		// Allowed only for already-plain SQLite files.
		hdr := make([]byte, 16)
		f, err := os.Open(raw)
		if err != nil {
			return sqlcipher.Stats{}, "", err
		}
		_, err = io.ReadFull(f, hdr)
		f.Close()
		if err != nil || string(hdr[:15]) != "SQLite format 3" {
			return sqlcipher.Stats{}, "", keys.ErrNoKey
		}
		return sqlcipher.Stats{}, out, os.Rename(raw, out)
	}
	st, err := sqlcipher.DecryptFile(raw, raw+"-wal", out, key)
	return st, out, err
}

// copyConsistent copies src and src-wal to dst/dst-wal read-only. Because the DB file only
// changes on checkpoint, it retries if src's size or mtime changed during the copy.
func copyConsistent(src, dst string, stat statFn) error {
	for try := 0; try < maxCopyTries; try++ {
		before, err := stat(src)
		if err != nil {
			return err
		}
		if err := copyFile(src, dst); err != nil {
			return err
		}
		if err := copyFile(src+"-wal", dst+"-wal"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		after, err := stat(src)
		if err != nil {
			return err
		}
		if before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) {
			return nil
		}
	}
	return fmt.Errorf("database kept changing during copy (%d attempts)", maxCopyTries)
}

func copyFile(src, dst string) error {
	in, err := os.OpenFile(src, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
