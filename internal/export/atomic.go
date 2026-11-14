package export

import (
	"fmt"
	"os"
)

// ErrExists is returned by WriteFileAtomic when dst already exists and force is false.
type ErrExists struct{ Path string }

func (e *ErrExists) Error() string {
	return fmt.Sprintf("export: %s already exists (use --force to overwrite)", e.Path)
}

// WriteFileAtomic calls write with a *os.File opened at dst+".tmp" (mode 0600), then renames it
// to dst only on success; on any error the temp file is removed and dst is left untouched. It
// refuses to proceed if dst already exists unless force is true (docs/SPEC.md §V.9).
func WriteFileAtomic(dst string, force bool, write func(*os.File) error) error {
	if !force {
		if _, err := os.Stat(dst); err == nil {
			return &ErrExists{Path: dst}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	tmp := dst + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
