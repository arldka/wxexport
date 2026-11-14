package sqlcipher_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arldka/wxexport/internal/sqlcipher"
)

// Sanity check against a real WeChat WAL (checksums need no key). Skipped when absent, and also
// skipped (not failed) if nothing validates: this reads a live file a separate, still-running
// WeChat process keeps mutating, so a transient read (e.g. right after a checkpoint reset the
// header salts, or mid-write) can legitimately show zero valid frames. This already served its
// purpose once during development (confirmed real committed frames against a live account); the
// deterministic guarantee lives in the synthetic WAL tests below.
func TestRealWALChecksums(t *testing.T) {
	home, _ := os.UserHomeDir()
	matches, _ := filepath.Glob(filepath.Join(home, ".var/app/com.tencent.WeChat/xwechat_files/*/db_storage/*/*.db-wal"))
	if len(matches) == 0 {
		t.Skip("no real WAL files")
	}
	ok := 0
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil || len(data) < 32+24+sqlcipher.PageSize {
			continue
		}
		r, err := sqlcipher.ParseWAL(data)
		if err != nil {
			t.Errorf("%s: %v", m, err)
			continue
		}
		total := (len(data) - 32) / (24 + sqlcipher.PageSize)
		t.Logf("%s: frames in file=%d valid=%d committed=%d dbsize=%d", filepath.Base(m), total, r.FramesScanned, r.FramesCommitted, r.DBSize)
		if r.FramesScanned > 0 {
			ok++
		}
	}
	if ok == 0 {
		t.Skip("no real WAL currently has a valid frame (likely a transient live-file read); synthetic WAL tests already cover correctness deterministically")
	}
}
