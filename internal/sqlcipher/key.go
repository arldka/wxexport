// Package sqlcipher implements read-only decryption of the SQLCipher 4 profile
// used by WCDB (WeChat 4.x): 4096-byte pages, AES-256-CBC, HMAC-SHA512,
// PBKDF2-HMAC-SHA512 with 256000 iterations and an 80-byte page reserve.
package sqlcipher

import (
	"crypto/pbkdf2"
	"crypto/sha512"
	"fmt"
)

const (
	PageSize = 4096
	Reserve  = 80
	SaltSize = 16
	IVSize   = 16
	HMACSize = 64
	KDFIter  = 256000
	// UsableSize is the number of bytes per page covered by encryption (page minus reserve).
	UsableSize = PageSize - Reserve
	macKDFIter = 2
	macSaltXOR = 0x3a
)

// Key holds the per-database encryption and MAC keys. It never prints its contents.
type Key struct {
	Enc [32]byte
	Mac [32]byte
}

// String redacts the key material (invariant: keys never reach logs or errors).
func (Key) String() string   { return "sqlcipher.Key(redacted)" }
func (Key) GoString() string { return "sqlcipher.Key(redacted)" }

// DeriveKey derives the per-database key from the 32-byte account passphrase
// and the database's 16-byte salt (first 16 bytes of the file).
func DeriveKey(passphrase, salt []byte) (Key, error) {
	enc, err := pbkdf2.Key(sha512.New, string(passphrase), salt, KDFIter, 32)
	if err != nil {
		return Key{}, fmt.Errorf("sqlcipher: derive enc key: %w", err)
	}
	return KeyFromEnc(enc, salt)
}

// KeyFromEnc builds a Key from an already derived 32-byte enc_key (as stored in the key cache).
func KeyFromEnc(enc, salt []byte) (Key, error) {
	if len(enc) != 32 {
		return Key{}, fmt.Errorf("sqlcipher: enc key must be 32 bytes, got %d", len(enc))
	}
	if len(salt) != SaltSize {
		return Key{}, fmt.Errorf("sqlcipher: salt must be %d bytes, got %d", SaltSize, len(salt))
	}
	macSalt := make([]byte, SaltSize)
	for i, b := range salt {
		macSalt[i] = b ^ macSaltXOR
	}
	mac, err := pbkdf2.Key(sha512.New, string(enc), macSalt, macKDFIter, 32)
	if err != nil {
		return Key{}, fmt.Errorf("sqlcipher: derive mac key: %w", err)
	}
	var k Key
	copy(k.Enc[:], enc)
	copy(k.Mac[:], mac)
	return k, nil
}
