package export

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

const sqliteSchema = `
CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE contact(username TEXT, display_name TEXT);
CREATE TABLE messages(
	local_id INTEGER, server_id INTEGER, shard TEXT, contact_username TEXT, sender_username TEXT,
	direction TEXT, timestamp_unix INTEGER, timestamp_iso TEXT, date TEXT, iso_week TEXT, hour INTEGER,
	type INTEGER, sub_type INTEGER, kind TEXT, text TEXT, char_count INTEGER, is_picture INTEGER,
	media_md5 TEXT, media_size INTEGER, media_duration_s REAL, call_media TEXT, call_outcome TEXT,
	call_duration_s INTEGER, url TEXT, title TEXT, raw_content TEXT
);
CREATE TABLE calls(
	local_id INTEGER, timestamp_iso TEXT, date TEXT, iso_week TEXT, direction TEXT,
	call_media TEXT, call_outcome TEXT, call_duration_s INTEGER
);
CREATE INDEX messages_date ON messages(date);
CREATE INDEX messages_iso_week ON messages(iso_week);
CREATE INDEX messages_kind ON messages(kind);
CREATE INDEX messages_direction ON messages(direction);
CREATE INDEX calls_date ON calls(date);
CREATE INDEX calls_iso_week ON calls(iso_week);
`

// WriteSQLite builds a complete SQLite export (meta/contact/messages/calls tables + indexes,
// see docs/SPEC.md §E) atomically: it is built entirely at dst+".tmp" and only renamed onto dst
// on full success, after the sqlite connection is closed (so dst is never a half-written or
// still-locked file).
func WriteSQLite(dst string, force bool, meta Meta, rows []Row) error {
	if !force {
		if _, err := os.Stat(dst); err == nil {
			return &ErrExists{Path: dst}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	tmp := dst + ".tmp"
	os.Remove(tmp)
	// Pre-create with 0600 so the database is never group/world-readable, even briefly
	// (SQLite would otherwise create it 0644 subject to umask).
	if f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err != nil {
		return err
	} else {
		f.Close()
	}
	if err := buildSQLite(tmp, meta, rows); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func buildSQLite(path string, meta Meta, rows []Row) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(sqliteSchema); err != nil {
		return fmt.Errorf("export: create schema: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES ('export', ?)`, string(metaJSON)); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO contact(username, display_name) VALUES (?, ?)`, meta.Contact.Username, meta.Contact.DisplayName); err != nil {
		return err
	}

	msgStmt, err := tx.Prepare(`INSERT INTO messages(
		local_id, server_id, shard, contact_username, sender_username, direction, timestamp_unix,
		timestamp_iso, date, iso_week, hour, type, sub_type, kind, text, char_count, is_picture,
		media_md5, media_size, media_duration_s, call_media, call_outcome, call_duration_s, url, title, raw_content
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer msgStmt.Close()

	callStmt, err := tx.Prepare(`INSERT INTO calls(local_id, timestamp_iso, date, iso_week, direction, call_media, call_outcome, call_duration_s)
		VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer callStmt.Close()

	for _, r := range rows {
		if _, err := msgStmt.Exec(
			r.LocalID, r.ServerID, r.Shard, r.ContactUsername, r.SenderUsername, r.Direction, r.TimestampUnix,
			r.TimestampISO, r.Date, r.ISOWeek, r.Hour, r.Type, r.SubType, r.Kind, r.Text, r.CharCount, r.IsPicture,
			r.MediaMD5, r.MediaSize, r.MediaDurationS, r.CallMedia, r.CallOutcome, r.CallDurationS, r.URL, r.Title, r.RawContent,
		); err != nil {
			return fmt.Errorf("export: insert message local_id=%d: %w", r.LocalID, err)
		}
		if r.IsCall() {
			if _, err := callStmt.Exec(r.LocalID, r.TimestampISO, r.Date, r.ISOWeek, r.Direction, r.CallMedia, r.CallOutcome, r.CallDurationS); err != nil {
				return fmt.Errorf("export: insert call local_id=%d: %w", r.LocalID, err)
			}
		}
	}

	return tx.Commit()
}
