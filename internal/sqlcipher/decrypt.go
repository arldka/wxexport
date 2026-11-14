package sqlcipher

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
)

// Stats summarises a DecryptFile run.
type Stats struct {
	Pages           int // pages decrypted from the main file
	WALPagesApplied int
	WALFrames       int // committed frames
	FinalPages      int
}

// ReadSalt returns the first 16 bytes of an encrypted database.
func ReadSalt(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	salt := make([]byte, SaltSize)
	if _, err := io.ReadFull(f, salt); err != nil {
		return nil, fmt.Errorf("sqlcipher: read salt of %s: %w", path, err)
	}
	return salt, nil
}

// DecryptFile decrypts the encrypted database at src into a plain SQLite file at dst
// (created 0600), then overlays the committed frames of the WAL at walPath (may be "" or
// nonexistent). Every page and every WAL frame is authenticated; any failure is an error
// naming the page. src and walPath are opened read-only.
func DecryptFile(src, walPath, dst string, key Key) (Stats, error) {
	var st Stats
	in, err := os.Open(src)
	if err != nil {
		return st, err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return st, err
	}
	if fi.Size() == 0 || fi.Size()%PageSize != 0 {
		return st, fmt.Errorf("sqlcipher: %s: size %d is not a positive multiple of %d", src, fi.Size(), PageSize)
	}

	out, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return st, err
	}
	defer out.Close()

	ebuf := make([]byte, PageSize)
	pbuf := make([]byte, PageSize)
	n := int(fi.Size() / PageSize)
	for pg := 1; pg <= n; pg++ {
		if _, err := io.ReadFull(in, ebuf); err != nil {
			return st, fmt.Errorf("sqlcipher: read page %d: %w", pg, err)
		}
		if err := key.DecryptPage(pbuf, ebuf, uint32(pg)); err != nil {
			return st, err
		}
		if _, err := out.Write(pbuf); err != nil {
			return st, err
		}
		st.Pages++
	}
	st.FinalPages = n

	if walPath != "" {
		wal, err := os.ReadFile(walPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return st, err
		}
		res, err := ParseWAL(wal)
		if err != nil {
			return st, err
		}
		pgs := make([]uint32, 0, len(res.Pages))
		for p := range res.Pages {
			pgs = append(pgs, p)
		}
		slices.Sort(pgs)
		for _, p := range pgs {
			if err := key.DecryptPage(pbuf, res.Pages[p], p); err != nil {
				var pe *PageError
				if errors.As(err, &pe) {
					pe.WAL = true
				} else if p == 1 {
					err = &PageError{Page: 1, WAL: true, Err: err}
				}
				return st, err
			}
			if _, err := out.WriteAt(pbuf, int64(p-1)*PageSize); err != nil {
				return st, err
			}
			st.WALPagesApplied++
		}
		st.WALFrames = res.FramesCommitted
		if res.DBSize > 0 {
			if err := out.Truncate(int64(res.DBSize) * PageSize); err != nil {
				return st, err
			}
			st.FinalPages = int(res.DBSize)
		}
	}
	return st, out.Close()
}
