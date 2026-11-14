package sqlcipher

import (
	"encoding/binary"
	"fmt"
)

const (
	walHeaderSize = 32
	walFrameHdr   = 24
	walMagicLE    = 0x377f0682 // checksums over little-endian words
	walMagicBE    = 0x377f0683 // checksums over big-endian words
)

// WALResult is the set of pages that must be overlaid on the database to reflect
// all committed WAL transactions. Page data are the still-encrypted frame bodies.
type WALResult struct {
	Pages           map[uint32][]byte // pgno -> encrypted page (latest committed version)
	DBSize          uint32            // database size in pages after the last commit (0 if none)
	FramesScanned   int               // valid frames read, committed or not
	FramesCommitted int
}

// WALChecksum is the SQLite WAL cumulative checksum.
func WALChecksum(bigEndian bool, data []byte, s0, s1 uint32) (uint32, uint32) {
	var bo binary.ByteOrder = binary.LittleEndian
	if bigEndian {
		bo = binary.BigEndian
	}
	for i := 0; i+8 <= len(data); i += 8 {
		s0 += bo.Uint32(data[i:]) + s1
		s1 += bo.Uint32(data[i+4:]) + s0
	}
	return s0, s1
}

// ParseWAL scans a (SQLCipher-encrypted) WAL file. Only frames belonging to
// transactions whose commit frame is valid are returned; scanning stops at the first
// invalid frame (salt or checksum mismatch, truncation), exactly like SQLite recovery.
// A WAL with a bad header is treated as empty, as SQLite does.
func ParseWAL(data []byte) (*WALResult, error) {
	res := &WALResult{Pages: map[uint32][]byte{}}
	if len(data) < walHeaderSize {
		return res, nil
	}
	magic := binary.BigEndian.Uint32(data[0:4])
	if magic != walMagicLE && magic != walMagicBE {
		return res, nil
	}
	big := magic == walMagicBE
	if ps := binary.BigEndian.Uint32(data[8:12]); ps != PageSize {
		return nil, fmt.Errorf("sqlcipher: WAL page size %d, expected %d", ps, PageSize)
	}
	s0, s1 := WALChecksum(big, data[:24], 0, 0)
	if s0 != binary.BigEndian.Uint32(data[24:28]) || s1 != binary.BigEndian.Uint32(data[28:32]) {
		return res, nil
	}
	salt1 := binary.BigEndian.Uint32(data[16:20])
	salt2 := binary.BigEndian.Uint32(data[20:24])

	pending := map[uint32][]byte{}
	frameSize := walFrameHdr + PageSize
	for off := walHeaderSize; off+frameSize <= len(data); off += frameSize {
		fh := data[off : off+walFrameHdr]
		body := data[off+walFrameHdr : off+frameSize]
		pgno := binary.BigEndian.Uint32(fh[0:4])
		commit := binary.BigEndian.Uint32(fh[4:8])
		if pgno == 0 ||
			binary.BigEndian.Uint32(fh[8:12]) != salt1 ||
			binary.BigEndian.Uint32(fh[12:16]) != salt2 {
			break
		}
		c0, c1 := WALChecksum(big, fh[:8], s0, s1)
		c0, c1 = WALChecksum(big, body, c0, c1)
		if c0 != binary.BigEndian.Uint32(fh[16:20]) || c1 != binary.BigEndian.Uint32(fh[20:24]) {
			break
		}
		s0, s1 = c0, c1
		res.FramesScanned++
		pending[pgno] = body
		if commit != 0 {
			for p, b := range pending {
				res.Pages[p] = b
			}
			clear(pending)
			res.DBSize = commit
			res.FramesCommitted = res.FramesScanned
		}
	}
	return res, nil
}
