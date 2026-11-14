package export

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCSVSafe(t *testing.T) {
	for in, want := range map[string]string{
		"=1+1": "'=1+1", "+cmd": "'+cmd", "-x": "'-x", "@SUM(A1)": "'@SUM(A1)", "\tx": "'\tx",
		"hello": "hello", "": "", "-5": "-5", "12": "12",
	} {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSQLiteFileMode(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "x.sqlite")
	if err := WriteSQLite(dst, false, Meta{}, nil); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
}

func TestDayRangeAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tzdata")
	}
	// 2024-10-27 has 25 hours in Paris: the inclusive end must be 23:59:59 local.
	_, to, _ := DayRange("", "2024-10-27", loc)
	if h, m, s := to.Clock(); h != 23 || m != 59 || s != 59 || to.Day() != 27 {
		t.Errorf("end = %v", to)
	}
}
