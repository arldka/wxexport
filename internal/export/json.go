package export

import (
	"encoding/json"
	"os"
)

type jsonDoc struct {
	Meta     Meta  `json:"meta"`
	Messages []Row `json:"messages"`
	Calls    []Row `json:"calls"`
}

// WriteJSON streams meta+rows (and the calls subset) to dst as one JSON document, atomically.
func WriteJSON(dst string, force bool, meta Meta, rows []Row) error {
	var calls []Row
	for _, r := range rows {
		if r.IsCall() {
			calls = append(calls, r)
		}
	}
	doc := jsonDoc{Meta: meta, Messages: rows, Calls: calls}
	return WriteFileAtomic(dst, force, func(f *os.File) error {
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(doc)
	})
}
