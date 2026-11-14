// Package msg decodes a raw WeChat message row into a normalized Message, per
// docs/SPEC.md §M (calibrated against real data — see docs/FORMAT.md).
package msg

import (
	"fmt"

	"github.com/klauspost/compress/zstd"
)

// Kind is the normalized message kind (docs/SPEC.md §M table).
type Kind string

const (
	KindText        Kind = "text"
	KindImage       Kind = "image"
	KindVoice       Kind = "voice"
	KindVideo       Kind = "video"
	KindSticker     Kind = "sticker"
	KindLocation    Kind = "location"
	KindContactCard Kind = "contact_card"
	KindLink        Kind = "link"
	KindFile        Kind = "file"
	KindQuote       Kind = "quote"
	KindForward     Kind = "forward"
	KindApplet      Kind = "applet"
	KindTransfer    Kind = "transfer"
	KindRedPacket   Kind = "red_packet"
	KindApp         Kind = "app"
	KindCall        Kind = "call"
	KindRecall      Kind = "recall"
	KindSystem      Kind = "system"
	KindUnknown     Kind = "unknown"
)

// WeChat local_type values (the low 32 bits of the 64-bit local_type column).
const (
	typeText     = 1
	typeImage    = 3
	typeVoice    = 34
	typeVideo    = 43
	typeSticker  = 47
	typeLocation = 48
	typeContact  = 42
	typeApp      = 49
	typeCall     = 50
	typeSystem   = 10000
)

// App-message subtypes (local_type's high 32 bits when type == 49).
const (
	subApplet1   = 4
	subLink      = 5
	subFile      = 6
	subApplet2   = 33
	subApplet3   = 36
	subForward   = 19
	subQuote     = 57
	subForward2  = 62
	subTransfer  = 2000
	subRedPacket = 2001
)

// CallOutcome classifies a call message.
type CallOutcome string

const (
	CallCompleted   CallOutcome = "completed"
	CallInterrupted CallOutcome = "interrupted" // connected, then cut off; still has a duration
	CallMissed      CallOutcome = "missed"
	CallCanceled    CallOutcome = "canceled"
	CallDeclined    CallOutcome = "declined"
	CallBusy        CallOutcome = "busy"
	CallUnknown     CallOutcome = "unknown"
)

// CallMedia classifies whether a call was voice or video.
type CallMedia string

const (
	CallVoice        CallMedia = "voice"
	CallVideo        CallMedia = "video"
	CallUnknownMedia CallMedia = "unknown"
)

// Media describes an attached image/voice/video/sticker/file.
type Media struct {
	MD5        string
	SizeBytes  int64
	DurationMS int64 // voice
	DurationS  int64 // video
}

// Call describes a decoded call (type 50).
type Call struct {
	Media     CallMedia
	Outcome   CallOutcome
	DurationS int64 // 0 unless Outcome is Completed or Interrupted
}

// Quote is a referenced ("quoted") message, for Kind == quote.
type Quote struct {
	FromUser    string
	DisplayName string
	Text        string
	CreateTime  int64
}

// Decoded is the normalized, parsed form of one raw message row.
type Decoded struct {
	Type, Sub int64
	Kind      Kind
	Text      string // human-readable text for Kind text/system/recall, or a short summary otherwise
	Media     *Media
	Call      *Call
	Location  *struct {
		Label    string
		Lat, Lng float64
	}
	Link *struct{ Title, URL string }
	File *struct {
		Title, Ext string
		SizeBytes  int64
	}
	Quote    *Quote
	AppTitle string // generic title for forward/applet/app/transfer kinds
}

// SplitLocalType splits the 64-bit local_type column into (type, sub).
func SplitLocalType(localType int64) (t, sub int64) {
	u := uint64(localType)
	return int64(u & 0xFFFFFFFF), int64(u >> 32)
}

const zstdMagic0, zstdMagic1, zstdMagic2, zstdMagic3 = 0x28, 0xb5, 0x2f, 0xfd
const maxDecompressed = 20 << 20 // 20 MiB cap, per spec

var zstdDecoder, _ = zstd.NewReader(nil, zstd.WithDecoderMaxMemory(maxDecompressed))

// IsZstd reports whether b looks zstd-compressed (checked independently of the DB's own flag,
// since the flag and the magic bytes should always agree, but we trust the magic as the source
// of truth if they ever disagree).
func IsZstd(b []byte) bool {
	return len(b) >= 4 && b[0] == zstdMagic0 && b[1] == zstdMagic1 && b[2] == zstdMagic2 && b[3] == zstdMagic3
}

// DecodeContent decodes a message_content/source cell: zstd if the WCDB flag says 4 or the bytes
// look like zstd, otherwise returned as-is (already UTF-8 text). ctFlag is the value of the
// column's WCDB_CT_* flag; ctFlagValid is whether that column existed/was non-NULL.
func DecodeContent(raw []byte, ctFlag int64, ctFlagValid bool) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	if (ctFlagValid && ctFlag == 4) || IsZstd(raw) {
		out, err := zstdDecoder.DecodeAll(raw, nil)
		if err != nil {
			return "", fmt.Errorf("msg: zstd decode: %w", err)
		}
		return string(out), nil
	}
	return string(raw), nil
}
