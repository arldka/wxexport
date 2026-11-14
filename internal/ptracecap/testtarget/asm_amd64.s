#include "textflag.h"

// func asmLoop()
TEXT ·asmLoop(SB), NOSPLIT, $0-0
	LEAQ ·testBuf(SB), SI
	MOVQ $32, DX
	CALL ·probeHook(SB)
	RET

// func probeHook()
// A single RET, so the breakpoint sits exactly at this function's entry and SI/DX, set by the
// caller just above, are still intact when it traps.
TEXT ·probeHook(SB), NOSPLIT, $0-0
	RET
