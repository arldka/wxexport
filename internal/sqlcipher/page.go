package sqlcipher

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrBadKey is returned when the page-1 HMAC does not validate: wrong key or not an encrypted DB.
var ErrBadKey = errors.New("sqlcipher: wrong key (page 1 HMAC mismatch)")

// ErrPageHMAC is wrapped in a PageError when a page other than page 1 fails authentication.
var ErrPageHMAC = errors.New("sqlcipher: page HMAC mismatch")

// PageError identifies the failing page (and whether it came from the WAL).
type PageError struct {
	Page uint32
	WAL  bool
	Err  error
}

func (e *PageError) Error() string {
	src := "page"
	if e.WAL {
		src = "WAL frame for page"
	}
	return fmt.Sprintf("sqlcipher: %s %d: %v", src, e.Page, e.Err)
}
func (e *PageError) Unwrap() error { return e.Err }

var sqliteMagic = []byte("SQLite format 3\x00")

func isZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

func (k Key) pageMAC(page []byte, pgno uint32) []byte {
	off := 0
	if pgno == 1 {
		off = SaltSize
	}
	h := hmac.New(sha512.New, k.Mac[:])
	h.Write(page[off : PageSize-HMACSize])
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], pgno)
	h.Write(n[:])
	return h.Sum(nil)
}

// VerifyPage checks the HMAC of an encrypted page. All-zero pages are considered valid
// (SQLCipher leaves never-written pages zeroed).
func (k Key) VerifyPage(page []byte, pgno uint32) bool {
	if len(page) != PageSize {
		return false
	}
	if pgno != 1 && isZero(page) {
		return true
	}
	return hmac.Equal(k.pageMAC(page, pgno), page[PageSize-HMACSize:])
}

// DecryptPage authenticates and decrypts one page into dst (len PageSize).
// Output is a plain SQLite page: page 1 begins with the SQLite magic, the 80-byte reserve is zeroed.
func (k Key) DecryptPage(dst, page []byte, pgno uint32) error {
	if len(page) != PageSize || len(dst) != PageSize {
		return fmt.Errorf("sqlcipher: bad page buffer size")
	}
	if pgno != 1 && isZero(page) {
		clear(dst)
		return nil
	}
	if !k.VerifyPage(page, pgno) {
		if pgno == 1 {
			return ErrBadKey
		}
		return &PageError{Page: pgno, Err: ErrPageHMAC}
	}
	off := 0
	if pgno == 1 {
		off = SaltSize
		copy(dst, sqliteMagic)
	}
	block, err := aes.NewCipher(k.Enc[:])
	if err != nil {
		return err
	}
	iv := page[UsableSize : UsableSize+IVSize]
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(dst[off:UsableSize], page[off:UsableSize])
	clear(dst[UsableSize:])
	return nil
}

// EncryptPage is the inverse of DecryptPage. plain is a PageSize buffer whose first
// UsableSize bytes (from byte 16 on, for page 1) are encrypted; salt is only used for page 1.
// It exists for building test fixtures and is not used on the export path.
func (k Key) EncryptPage(dst, plain []byte, pgno uint32, salt, iv []byte) error {
	if len(plain) != PageSize || len(dst) != PageSize || len(iv) != IVSize {
		return fmt.Errorf("sqlcipher: bad buffer sizes")
	}
	off := 0
	if pgno == 1 {
		off = SaltSize
		if len(salt) != SaltSize {
			return fmt.Errorf("sqlcipher: page 1 needs a %d-byte salt", SaltSize)
		}
		copy(dst, salt)
	}
	block, err := aes.NewCipher(k.Enc[:])
	if err != nil {
		return err
	}
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(dst[off:UsableSize], plain[off:UsableSize])
	copy(dst[UsableSize:], iv)
	copy(dst[PageSize-HMACSize:], k.pageMAC(dst, pgno))
	return nil
}
