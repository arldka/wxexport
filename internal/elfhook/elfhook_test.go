package elfhook

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

const textVA = 0x1000

// lea writes `<op3> disp32` at text[pos:] so that it targets va.
func lea(text []byte, pos int, op []byte, va uint64) {
	copy(text[pos:], op)
	disp := int32(int64(va) - int64(textVA+uint64(pos)+7))
	binary.LittleEndian.PutUint32(text[pos+3:], uint32(disp))
}

func blank(n int) []byte { return bytes.Repeat([]byte{0x90}, n) }

func TestAnalyzeFindsLowestPrologue(t *testing.T) {
	const anchor, cfg = 0x9000, 0xA000
	text := blank(0x4000)
	// registration site: lea rdi,cfg ; lea rsi,anchor (adjacent)
	lea(text, 0x100, leaRdi, cfg)
	lea(text, 0x107, leaRsi, anchor)
	// two functions referencing cfg; the lower one must win
	copy(text[0x1000:], prologue)
	lea(text, 0x1200, leaRsi, cfg)
	copy(text[0x2000:], prologue)
	lea(text, 0x2300, leaRsi, cfg)
	r, err := Analyze(text, textVA, anchor)
	if err != nil {
		t.Fatal(err)
	}
	if r.HookVA != textVA+0x1000 || len(r.Candidates) != 2 || r.ConfigVAs[0] != cfg {
		t.Fatalf("%+v", r)
	}
}

func TestAnalyzeRejectsMissingLeaRdi(t *testing.T) {
	text := blank(0x1000)
	lea(text, 0x107, leaRsi, 0x9000) // no lea rdi 7 bytes earlier
	if _, err := Analyze(text, textVA, 0x9000); err != ErrNoConfig {
		t.Fatalf("got %v", err)
	}
}

func TestAnalyzeWalkBackLimit(t *testing.T) {
	const anchor, cfg = 0x9000, 0xA000
	text := blank(0x4000)
	lea(text, 0x100, leaRdi, cfg)
	lea(text, 0x107, leaRsi, anchor)
	copy(text[0x1000:], prologue)
	lea(text, 0x1000+maxWalkBack+0x10, leaRsi, cfg) // prologue is further than 0x500 behind
	if _, err := Analyze(text, textVA, anchor); err != ErrNoHook {
		t.Fatalf("got %v", err)
	}
	lea(text, 0x1000+maxWalkBack-0x10, leaRsi, cfg) // within range now
	if _, err := Analyze(text, textVA, anchor); err != nil {
		t.Fatalf("got %v", err)
	}
}

func TestAnalyzeNegativeDisplacementAndNearestPrologue(t *testing.T) {
	const anchor, cfg = 0x1800, 0x1900 // targets inside text VA range -> negative disps
	text := blank(0x2000)
	lea(text, 0x500, leaRdi, cfg)
	lea(text, 0x507, leaRsi, anchor)
	copy(text[0x600:], prologue)
	copy(text[0x700:], prologue) // nearer to the reference
	lea(text, 0x780, leaRsi, cfg)
	r, err := Analyze(text, textVA, anchor)
	if err != nil || r.HookVA != textVA+0x700 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestFindAnchorRequiresStringStart(t *testing.T) {
	// A synthetic ELF is overkill; use the running test binary only to check error path.
	f, err := elf.Open(os.Args[0])
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	if _, err := FindAnchor(f); err != ErrNoAnchor {
		t.Fatalf("test binary should not contain the anchor... got %v", err)
	}
}

func TestRealBinary(t *testing.T) {
	m, _ := filepath.Glob("/var/lib/flatpak/app/com.tencent.WeChat/x86_64/stable/*/files/extra/wechat/wechat")
	if len(m) == 0 {
		t.Skip("WeChat flatpak not installed")
	}
	r, err := FindHook(m[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("hook=%#x anchor=%#x configs=%x candidates=%x firstLoad=%#x", r.HookVA, r.AnchorVA, r.ConfigVAs, r.Candidates, r.FirstLoadVaddr)
	f, _ := elf.Open(m[0])
	defer f.Close()
	text := f.Section(".text")
	if r.HookVA < text.Addr || r.HookVA >= text.Addr+text.Size {
		t.Fatalf("hook outside .text")
	}
	b := make([]byte, 3)
	if _, err := text.ReadAt(b, int64(r.HookVA-text.Addr)); err != nil || !bytes.Equal(b, prologue) {
		t.Fatalf("hook does not start with push rbp; push r15: % x", b)
	}
}
