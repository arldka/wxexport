// Package elfhook locates, by static analysis of the WeChat ELF, the function that receives
// the WCDB cipher passphrase at login (WeChat >= 4.1 no longer keeps the derived key around).
//
// Algorithm (x86-64):
//  1. find the string "com.Tencent.WCDB.Config.Cipher" in a non-executable section (anchorVA);
//  2. in .text find `lea rsi,[rip+d]` (48 8D 35) targeting anchorVA, preceded 7 bytes earlier by
//     `lea rdi,[rip+d]` (48 8D 3D); the rdi target is the config struct (cfgVA);
//  3. find every `lea rsi` targeting cfgVA; walk back <= 0x500 bytes to the prologue
//     `push rbp; push r15` (55 41 57);
//  4. the lowest function start found is the hook.
package elfhook

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

const (
	Anchor      = "com.Tencent.WCDB.Config.Cipher"
	maxWalkBack = 0x500
	leaLen      = 7
)

var (
	leaRsi   = []byte{0x48, 0x8d, 0x35}
	leaRdi   = []byte{0x48, 0x8d, 0x3d}
	prologue = []byte{0x55, 0x41, 0x57}
)

var (
	ErrNoAnchor = errors.New("elfhook: anchor string not found in binary")
	ErrNoConfig = errors.New("elfhook: no `lea rdi/lea rsi` pair referencing the anchor in .text")
	ErrNoHook   = errors.New("elfhook: no function prologue found before references to the cipher config struct")
)

// Result describes the located hook.
type Result struct {
	HookVA     uint64 // virtual address of the function start
	AnchorVA   uint64
	ConfigVAs  []uint64
	Candidates []uint64 // all function starts found (sorted)
	// FirstLoadVaddr is p_vaddr of the PT_LOAD containing file offset 0, so that
	// runtime address = mapBase + HookVA - FirstLoadVaddr (mapBase = first mapping with offset 0).
	FirstLoadVaddr uint64
}

func target(text []byte, textVA uint64, i int) uint64 {
	disp := int32(binary.LittleEndian.Uint32(text[i+3:]))
	return uint64(int64(textVA) + int64(i) + leaLen + int64(disp))
}

// refs returns the offsets of every `lea <reg>,[rip+d]` (given opcode prefix) in text whose target is va.
func refs(text []byte, textVA uint64, op []byte, va uint64) []int {
	var out []int
	for off := 0; ; {
		j := bytes.Index(text[off:], op)
		if j < 0 {
			return out
		}
		i := off + j
		if i+leaLen <= len(text) && target(text, textVA, i) == va {
			out = append(out, i)
		}
		off = i + 1
	}
}

// Analyze runs steps 2-4 on a .text image.
func Analyze(text []byte, textVA, anchorVA uint64) (Result, error) {
	res := Result{AnchorVA: anchorVA}
	seenCfg := map[uint64]bool{}
	for _, i := range refs(text, textVA, leaRsi, anchorVA) {
		if i < leaLen || !bytes.HasPrefix(text[i-leaLen:], leaRdi) {
			continue
		}
		cfg := target(text, textVA, i-leaLen)
		if !seenCfg[cfg] {
			seenCfg[cfg] = true
			res.ConfigVAs = append(res.ConfigVAs, cfg)
		}
	}
	if len(res.ConfigVAs) == 0 {
		return res, ErrNoConfig
	}
	seenFn := map[uint64]bool{}
	for _, cfg := range res.ConfigVAs {
		for _, i := range refs(text, textVA, leaRsi, cfg) {
			lo := max(0, i-maxWalkBack)
			if k := bytes.LastIndex(text[lo:i], prologue); k >= 0 {
				va := textVA + uint64(lo+k)
				if !seenFn[va] {
					seenFn[va] = true
					res.Candidates = append(res.Candidates, va)
				}
			}
		}
	}
	if len(res.Candidates) == 0 {
		return res, ErrNoHook
	}
	sort.Slice(res.Candidates, func(a, b int) bool { return res.Candidates[a] < res.Candidates[b] })
	res.HookVA = res.Candidates[0]
	return res, nil
}

// FindAnchor returns the VA of the NUL-delimited anchor string in any allocated, non-executable section.
func FindAnchor(f *elf.File) (uint64, error) {
	needle := append([]byte{0}, append([]byte(Anchor), 0)...)
	for _, s := range f.Sections {
		if s.Type != elf.SHT_PROGBITS || s.Flags&elf.SHF_ALLOC == 0 || s.Flags&elf.SHF_EXECINSTR != 0 {
			continue
		}
		data, err := s.Data()
		if err != nil {
			return 0, err
		}
		if i := bytes.Index(data, needle); i >= 0 {
			return s.Addr + uint64(i) + 1, nil
		}
	}
	return 0, ErrNoAnchor
}

// FindHook analyses the ELF at path.
func FindHook(path string) (Result, error) {
	f, err := elf.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	if f.Machine != elf.EM_X86_64 {
		return Result{}, fmt.Errorf("elfhook: unsupported machine %v (x86-64 only)", f.Machine)
	}
	anchor, err := FindAnchor(f)
	if err != nil {
		return Result{}, err
	}
	text := f.Section(".text")
	if text == nil {
		return Result{}, errors.New("elfhook: no .text section")
	}
	data, err := text.Data()
	if err != nil {
		return Result{}, err
	}
	res, err := Analyze(data, text.Addr, anchor)
	if err != nil {
		return res, err
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && p.Off == 0 {
			res.FirstLoadVaddr = p.Vaddr
			break
		}
	}
	return res, nil
}
