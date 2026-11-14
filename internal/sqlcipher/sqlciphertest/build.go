// Package sqlciphertest builds encrypted database and WAL fixtures (the inverse of
// package sqlcipher) so tests can run without a real WeChat install.
package sqlciphertest

import (
	"encoding/binary"
	"fmt"

	"github.com/arldka/wxexport/internal/sqlcipher"
)

// iv derives a deterministic IV so fixtures are reproducible.
func iv(pgno uint32, tag byte) []byte {
	b := make([]byte, sqlcipher.IVSize)
	for i := range b {
		b[i] = byte(i) ^ tag ^ byte(pgno*7)
	}
	return b
}

// EncryptDB encrypts a plaintext image made of 4096-byte pages whose last 80 bytes of each
// page are the (unused) reserve. Bytes 0..16 of page 1 are replaced by salt.
func EncryptDB(plain []byte, key sqlcipher.Key, salt []byte) ([]byte, error) {
	if len(plain)%sqlcipher.PageSize != 0 || len(plain) == 0 {
		return nil, fmt.Errorf("plain image must be a positive multiple of %d", sqlcipher.PageSize)
	}
	out := make([]byte, len(plain))
	for i := 0; i < len(plain)/sqlcipher.PageSize; i++ {
		lo, hi := i*sqlcipher.PageSize, (i+1)*sqlcipher.PageSize
		if err := key.EncryptPage(out[lo:hi], plain[lo:hi], uint32(i+1), salt, iv(uint32(i+1), 0xA5)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// WALFrame describes one frame to write. Commit is the db size in pages for commit frames, else 0.
type WALFrame struct {
	Pgno   uint32
	Commit uint32
	Plain  []byte // PageSize bytes
}

// WALOpts controls header fields; zero value is little-endian with salts 1,2.
type WALOpts struct {
	BigEndian    bool
	Salt1, Salt2 uint32
	// FrameSalt overrides salts for frame index (0-based) -> pair, to forge mismatches.
	FrameSalt map[int][2]uint32
	// CorruptChecksum flips the stored checksum of the given frame index.
	CorruptChecksum map[int]bool
}

// BuildWAL encrypts frames with key/salt and returns a complete WAL file image.
func BuildWAL(frames []WALFrame, key sqlcipher.Key, dbSalt []byte, o WALOpts) ([]byte, error) {
	if o.Salt1 == 0 && o.Salt2 == 0 {
		o.Salt1, o.Salt2 = 1, 2
	}
	magic := uint32(0x377f0682)
	if o.BigEndian {
		magic = 0x377f0683
	}
	out := make([]byte, 32)
	binary.BigEndian.PutUint32(out[0:], magic)
	binary.BigEndian.PutUint32(out[4:], 3007000)
	binary.BigEndian.PutUint32(out[8:], sqlcipher.PageSize)
	binary.BigEndian.PutUint32(out[12:], 0)
	binary.BigEndian.PutUint32(out[16:], o.Salt1)
	binary.BigEndian.PutUint32(out[20:], o.Salt2)
	s0, s1 := sqlcipher.WALChecksum(o.BigEndian, out[:24], 0, 0)
	binary.BigEndian.PutUint32(out[24:], s0)
	binary.BigEndian.PutUint32(out[28:], s1)

	for i, f := range frames {
		enc := make([]byte, sqlcipher.PageSize)
		if err := key.EncryptPage(enc, f.Plain, f.Pgno, dbSalt, iv(f.Pgno, byte(0x10+i))); err != nil {
			return nil, err
		}
		fh := make([]byte, 24)
		binary.BigEndian.PutUint32(fh[0:], f.Pgno)
		binary.BigEndian.PutUint32(fh[4:], f.Commit)
		sa, sb := o.Salt1, o.Salt2
		if s, ok := o.FrameSalt[i]; ok {
			sa, sb = s[0], s[1]
		}
		binary.BigEndian.PutUint32(fh[8:], sa)
		binary.BigEndian.PutUint32(fh[12:], sb)
		s0, s1 = sqlcipher.WALChecksum(o.BigEndian, fh[:8], s0, s1)
		s0, s1 = sqlcipher.WALChecksum(o.BigEndian, enc, s0, s1)
		c0, c1 := s0, s1
		if o.CorruptChecksum[i] {
			c0 ^= 0xdeadbeef
		}
		binary.BigEndian.PutUint32(fh[16:], c0)
		binary.BigEndian.PutUint32(fh[20:], c1)
		out = append(out, fh...)
		out = append(out, enc...)
	}
	return out, nil
}
