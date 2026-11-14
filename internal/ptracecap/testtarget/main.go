// Command testtarget is a throwaway fixture for ptracecap's integration test: it repeatedly
// calls a one-instruction assembly function with rsi/rdx set to a known 32-byte buffer, exactly
// the shape ptracecap.Capture looks for (candidate form (a) in docs/SPEC.md §K.4). It is not part
// of wxexport; it exists only so the test can attach to a real process instead of a mock.
package main

import "time"

// testBuf is read by the test over ptrace; its content is also asserted by the test directly.
var testBuf = [32]byte{
	0xaa, 0xbb, 0xcc, 0xdd, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12,
	13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 0xee,
}

// asmLoop (internal/ptracecap/testtarget/asm_amd64.s) sets SI=&testBuf, DX=32, then calls
// probeHook, whose entry point is where the test places its breakpoint.
func asmLoop()

func probeHook()

func main() {
	for {
		asmLoop()
		time.Sleep(5 * time.Millisecond)
	}
}
