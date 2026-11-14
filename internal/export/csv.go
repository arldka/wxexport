package export

import (
	"encoding/csv"
	"os"
)

// utf8BOM is U+FEFF's UTF-8 encoding, built from byte values (not a string escape) because the
// Go toolchain rejects a literal BOM character embedded anywhere in source text.
var utf8BOM = string([]byte{0xEF, 0xBB, 0xBF})

// WriteMessagesCSV writes the full messages.csv (RFC 4180, UTF-8), atomically. If includeRaw,
// a trailing raw_content column is added. If excelBOM, a UTF-8 BOM is written first (helps
// Excel auto-detect UTF-8 on Windows).
func WriteMessagesCSV(dst string, force, includeRaw, excelBOM bool, rows []Row) error {
	cols := append([]string{}, Columns...)
	if includeRaw {
		cols = append(cols, "raw_content")
	}
	return WriteFileAtomic(dst, force, func(f *os.File) error {
		if excelBOM {
			if _, err := f.WriteString(utf8BOM); err != nil {
				return err
			}
		}
		w := csv.NewWriter(f)
		if err := w.Write(cols); err != nil {
			return err
		}
		for _, r := range rows {
			if err := w.Write(r.Values(includeRaw)); err != nil {
				return err
			}
		}
		w.Flush()
		return w.Error()
	})
}

// WriteCallsCSV writes calls.csv (only call rows, CallColumns order), atomically.
func WriteCallsCSV(dst string, force, excelBOM bool, rows []Row) error {
	return WriteFileAtomic(dst, force, func(f *os.File) error {
		if excelBOM {
			if _, err := f.WriteString(utf8BOM); err != nil {
				return err
			}
		}
		w := csv.NewWriter(f)
		if err := w.Write(CallColumns); err != nil {
			return err
		}
		for _, r := range rows {
			if !r.IsCall() {
				continue
			}
			if err := w.Write(r.CallValues()); err != nil {
				return err
			}
		}
		w.Flush()
		return w.Error()
	})
}
