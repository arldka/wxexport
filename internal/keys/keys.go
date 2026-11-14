// Package keys manages the cached WeChat passphrase / per-database keys and validates
// them against the actual database files.
package keys

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/arldka/wxexport/internal/sqlcipher"
)

const cacheVersion = 1

// ErrNoKey is returned by Resolve when no database could be keyed.
var ErrNoKey = errors.New("keys: no valid key for any database; run `wxexport key capture`")

type Entry struct {
	SaltHex   string `json:"salt_hex"`
	EncKeyHex string `json:"enc_key_hex"`
}

// Cache is the on-disk key store (mode 0600).
type Cache struct {
	Version       int              `json:"version"`
	Account       string           `json:"account"`
	WeChatVersion string           `json:"wechat_version,omitempty"`
	BinarySHA256  string           `json:"binary_sha256,omitempty"`
	HookOffset    uint64           `json:"hook_offset,omitempty"`
	PassphraseHex string           `json:"passphrase_hex,omitempty"`
	CapturedAt    time.Time        `json:"captured_at,omitempty"`
	DBs           map[string]Entry `json:"dbs,omitempty"`
}

// Load returns (nil, nil) if the file does not exist.
func Load(path string) (*Cache, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c Cache
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("keys: parse %s: %w", path, err)
	}
	if c.Version != cacheVersion {
		return nil, fmt.Errorf("keys: %s has unsupported version %d", path, c.Version)
	}
	return &c, nil
}

// Save writes atomically with mode 0600 in a 0700 directory.
func (c *Cache) Save(path string) error {
	c.Version = cacheVersion
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Passphrase decodes the cached passphrase, or nil.
func (c *Cache) Passphrase() []byte {
	if c == nil || c.PassphraseHex == "" {
		return nil
	}
	b, err := hex.DecodeString(c.PassphraseHex)
	if err != nil || len(b) != 32 {
		return nil
	}
	return b
}

// TryPassphrase derives the key for a database from its first page and reports whether it validates.
func TryPassphrase(pass, page1 []byte) (sqlcipher.Key, bool) {
	if len(page1) < sqlcipher.PageSize {
		return sqlcipher.Key{}, false
	}
	k, err := sqlcipher.DeriveKey(pass, page1[:sqlcipher.SaltSize])
	if err != nil || !k.VerifyPage(page1[:sqlcipher.PageSize], 1) {
		return sqlcipher.Key{}, false
	}
	return k, true
}

type Status string

const (
	StatusCached  Status = "cached"      // cached enc_key validated
	StatusDerived Status = "derived"     // derived from passphrase and validated
	StatusPlain   Status = "unencrypted" // file is already plain SQLite
	StatusMissing Status = "no-key"
	StatusError   Status = "error"
)

// DB identifies one database file to key. Rel is the path relative to db_storage.
type DB struct {
	Rel  string
	Path string
}

type Result struct {
	DB     DB
	Status Status
	Err    error
}

// Resolve finds a valid key for every database: cached enc_key first, then re-derivation from the
// cached passphrase. It updates c.DBs in place (caller decides whether to Save) and returns
// ErrNoKey if nothing at all validated. Results are returned for every input DB.
func Resolve(c *Cache, dbs []DB) (map[string]sqlcipher.Key, []Result, error) {
	out := map[string]sqlcipher.Key{}
	results := make([]Result, len(dbs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	pass := c.Passphrase()
	for i, d := range dbs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := Result{DB: d, Status: StatusMissing}
			page1, err := readPage1(d.Path)
			switch {
			case err != nil:
				r.Status, r.Err = StatusError, err
			case string(page1[:15]) == "SQLite format 3":
				r.Status = StatusPlain
			default:
				salt := page1[:sqlcipher.SaltSize]
				if c != nil {
					if e, ok := c.DBs[d.Rel]; ok && e.SaltHex == hex.EncodeToString(salt) {
						if enc, err := hex.DecodeString(e.EncKeyHex); err == nil {
							if k, err := sqlcipher.KeyFromEnc(enc, salt); err == nil && k.VerifyPage(page1, 1) {
								mu.Lock()
								out[d.Rel] = k
								mu.Unlock()
								r.Status = StatusCached
							}
						}
					}
				}
				if r.Status == StatusMissing && pass != nil {
					if k, ok := TryPassphrase(pass, page1); ok {
						mu.Lock()
						out[d.Rel] = k
						if c.DBs == nil {
							c.DBs = map[string]Entry{}
						}
						c.DBs[d.Rel] = Entry{SaltHex: hex.EncodeToString(salt), EncKeyHex: hex.EncodeToString(k.Enc[:])}
						mu.Unlock()
						r.Status = StatusDerived
					}
				}
			}
			results[i] = r
		}()
	}
	wg.Wait()
	if len(out) == 0 {
		any := false
		for _, r := range results {
			if r.Status == StatusPlain {
				any = true
			}
		}
		if !any {
			return out, results, ErrNoKey
		}
	}
	return out, results, nil
}

func readPage1(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, sqlcipher.PageSize)
	if _, err := io.ReadFull(f, b); err != nil {
		return nil, fmt.Errorf("read first page of %s: %w", filepath.Base(path), err)
	}
	return b, nil
}
