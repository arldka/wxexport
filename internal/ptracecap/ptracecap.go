// Package ptracecap implements the attach/breakpoint/read step described in docs/SPEC.md §K.3-5.
//
// Scope and authorization. wxexport is a personal export tool: it lets a WeChat account's own
// owner pull their own 1:1 message and call history off their own machine. On WeChat >= 4.1 the
// database passphrase WeChat itself derives at login is no longer left anywhere in process memory
// in a form that can be found by scanning (see docs/SPEC.md §K); the only remaining read-only way
// to learn it is to catch it once, at the moment WeChat's own code receives it, with a single
// breakpoint on WeChat's own cipher-configuration function. That is what this package does, and
// nothing else:
//   - it only ever targets a WeChat process the *caller* launched (via `wxexport key capture`),
//     normally a direct child, never an arbitrary or unrelated process;
//   - it sets exactly one breakpoint, at an address supplied by the caller (see package elfhook),
//     reads only the fixed-shape 32-byte value described in SPEC.md §K.4, and restores the
//     original instruction byte before resuming;
//   - every candidate value is checked against an HMAC the caller supplies (it must reproduce a
//     checksum already present in WeChat's own database files) before it is ever returned, kept,
//     or written to disk;
//   - it never injects, patches, or modifies any behaviour of the target beyond the single
//     restored breakpoint byte, and it detaches and leaves the process running whether or not the
//     capture succeeds.
//
// This is not a general code-injection, memory-scraping, or credential-harvesting primitive, and
// it is not invoked automatically; it exists solely so wxexport can open databases the logged-in
// WeChat session already has the right to read, using the same passphrase WeChat itself holds.
package ptracecap

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"time"

	"golang.org/x/sys/unix"

	"github.com/arldka/wxexport/internal/procfs"
)

const bpOpcode = 0xCC

var (
	// ErrTimeout is returned when the breakpoint was not hit (and validated) within opt.Timeout.
	ErrTimeout = errors.New("ptracecap: timed out waiting for the breakpoint to be hit")
	// ErrExited is returned if the target process disappears while we are attached.
	ErrExited = errors.New("ptracecap: target process exited before the breakpoint was hit")
	// ErrNoTasks is returned if the target has no threads to attach to.
	ErrNoTasks = errors.New("ptracecap: target process has no threads")
)

// Validate reports whether candidate (always 32 bytes) is the right passphrase, typically by
// deriving a key from it and checking a database's own authentication tag.
type Validate func(candidate []byte) bool

// Options configures Capture.
type Options struct {
	// BreakpointAddr is the absolute runtime (not file) address of the function entry point to
	// break on, computed by the caller as mapBase + fileVA - firstLoadVaddr (see elfhook.Result).
	BreakpointAddr uint64
	Timeout        time.Duration
	Validate       Validate
	// Tasks lists the thread ids of pid; overridable for tests. Default: /proc/<pid>/task.
	Tasks func(pid int) []int
}

// Result is what Capture found.
type Result struct {
	Passphrase []byte
	Candidates int // number of candidate values seen (validated or not) before success
}

// Capture attaches to every thread of pid, arms a software breakpoint at opt.BreakpointAddr, and
// waits for it to be hit. On each hit it extracts both candidate shapes described in
// docs/SPEC.md §K.4 ((a) a raw pointer+length in registers, (b) a small struct pointed to by a
// register) and validates each with opt.Validate. A validated hit ends the capture; a rejected
// hit is stepped over and the breakpoint is re-armed, so later calls (e.g. for other databases)
// are observed as well. Capture always restores the original instruction byte and detaches every
// thread, leaving the target running, including on error, timeout, or ctx cancellation.
//
// All ptrace operations for this call happen on one OS thread (runtime.LockOSThread), as the
// kernel requires: a tracee must be waited on and operated on by the specific thread that
// attached it. Every memory read/write and every detach also requires its target tid to be in
// ptrace-stop state at that instant (the kernel returns ESRCH otherwise); this package tracks
// that explicitly rather than assuming it (see tracer.resumed and ensureStopped).
func Capture(ctx context.Context, pid int, opt Options) (Result, error) {
	if opt.Timeout == 0 {
		opt.Timeout = 180 * time.Second
	}
	if opt.Tasks == nil {
		opt.Tasks = defaultTasks
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	tr := &tracer{opt: opt}
	// Registered before attachAll runs: if it attaches some threads and then fails on a later
	// one, the ones it did attach must still be resumed and detached, or they are stuck in
	// ptrace-stop forever (the OS thread that attached them is pooled by the Go runtime, not
	// destroyed, so the kernel's "tracer died" auto-detach never fires to rescue them).
	defer tr.detachAll()
	if err := tr.attachAll(pid); err != nil {
		return Result{}, err
	}

	if err := tr.arm(tr.pid); err != nil {
		return Result{}, err
	}
	defer tr.cleanupBreakpoint()
	// Every thread is still stopped from PTRACE_ATTACH; resume them now that the breakpoint is in.
	tr.continueAll()

	deadline := time.Now().Add(opt.Timeout)
	for {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if time.Now().After(deadline) {
			return Result{}, ErrTimeout
		}
		res, done, err := tr.waitOnce()
		if err != nil {
			return Result{}, err
		}
		if done {
			return res, nil
		}
	}
}

func defaultTasks(pid int) []int { return procfs.Default().Tasks(pid) }

type tracer struct {
	pid            int
	opt            Options
	attached       map[int]bool // tid -> ptrace options already set
	orig           []byte       // original byte at BreakpointAddr
	armed          bool
	resumed        bool // continueAll has run: attached tids are no longer assumed stopped
	candidatesSeen int
}

func (tr *tracer) attachAll(pid int) error {
	tr.pid = pid
	tr.attached = map[int]bool{}
	tasks := tr.opt.Tasks(pid)
	if len(tasks) == 0 {
		return ErrNoTasks
	}
	for _, tid := range tasks {
		if err := tr.attachOne(tid); err != nil {
			return fmt.Errorf("ptracecap: attach thread %d: %w", tid, err)
		}
	}
	return nil
}

const ptraceOpts = unix.PTRACE_O_TRACECLONE | unix.PTRACE_O_TRACEFORK | unix.PTRACE_O_TRACEVFORK | unix.PTRACE_O_TRACEEXEC

func (tr *tracer) attachOne(tid int) error {
	if err := unix.PtraceAttach(tid); err != nil {
		return err
	}
	var ws unix.WaitStatus
	if _, err := unix.Wait4(tid, &ws, 0, nil); err != nil {
		return err
	}
	if err := unix.PtraceSetOptions(tid, ptraceOpts); err != nil {
		return err
	}
	tr.attached[tid] = true
	return nil
}

func (tr *tracer) continueAll() {
	tr.resumed = true
	for tid := range tr.attached {
		_ = unix.PtraceCont(tid, 0)
	}
}

// waitStoppedBounded polls (WNOHANG) for up to timeout for tid to report a ptrace-stop,
// without blocking forever: tid may already be stopped (nothing new will ever arrive) or may
// have exited, and a plain blocking Wait4 would hang in either case.
func waitStoppedBounded(tid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var ws unix.WaitStatus
		if wp, _ := unix.Wait4(tid, &ws, unix.WNOHANG, nil); wp == tid {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// ensureStopped makes a best effort to have tid in ptrace-stop state, which PTRACE_POKETEXT and
// PTRACE_DETACH both require. If the tracer never resumed tid, it is already stopped (from
// PTRACE_ATTACH or a trap) and nothing is done. Otherwise a real SIGSTOP is sent and awaited with
// a bound, since trying to stop an already-stopped tracee would otherwise wait forever.
func (tr *tracer) ensureStopped(tid int) {
	if !tr.resumed {
		return
	}
	if unix.Kill(tid, unix.SIGSTOP) != nil {
		return // already gone
	}
	waitStoppedBounded(tid, 200*time.Millisecond)
}

const maxDetachAttempts = 5

// detachAll detaches (and thereby resumes) every attached thread, leaving the target running.
// Detach requires the tid to be stopped; most tids are running after continueAll, so a direct
// attempt is tried first (covers tids that happen to be stopped, e.g. mid-trap) and, on failure,
// ensureStopped is used before retrying. A single stop-then-retry can still race a tid that is
// resumed by something else in the same instant (observed: a sibling thread's own SIGSTOP
// causing a transient group-stop/continue that another tid's detach attempt straddles), so this
// retries a bounded number of times rather than once.
func (tr *tracer) detachAll() {
	for tid := range tr.attached {
		var err error
		for attempt := 0; attempt < maxDetachAttempts; attempt++ {
			if err = unix.PtraceDetach(tid); err == nil {
				break
			}
			tr.ensureStopped(tid)
		}
	}
}

// arm writes the breakpoint byte into tid's (i.e. the process's, since text is shared) memory.
// tid must currently be ptrace-stopped; callers either know this structurally (the initial arm,
// before continueAll ever runs) or have just observed it (re-arming right after a trap, before
// that specific tid is continued again).
func (tr *tracer) arm(tid int) error {
	if tr.orig == nil {
		tr.orig = make([]byte, 1)
		if _, err := unix.PtracePeekText(tid, uintptr(tr.opt.BreakpointAddr), tr.orig); err != nil {
			return fmt.Errorf("ptracecap: read original instruction byte: %w", err)
		}
	}
	if _, err := unix.PtracePokeText(tid, uintptr(tr.opt.BreakpointAddr), []byte{bpOpcode}); err != nil {
		return fmt.Errorf("ptracecap: arm breakpoint: %w", err)
	}
	tr.armed = true
	return nil
}

// cleanupBreakpoint removes the breakpoint byte if it is still armed when Capture is about to
// return (timeout, cancellation, or an error partway through): tr.pid may be running freely at
// this point, so ensureStopped is used before poking, and the thread is left stopped (detachAll,
// which runs right after via its own defer, will then detach it directly).
func (tr *tracer) cleanupBreakpoint() {
	if !tr.armed || tr.orig == nil {
		return
	}
	tr.ensureStopped(tr.pid)
	if _, err := unix.PtracePokeText(tr.pid, uintptr(tr.opt.BreakpointAddr), tr.orig); err == nil {
		tr.armed = false
	}
}

// restoreAndStep removes the breakpoint byte, rewinds tid's PC to the real instruction, and
// single-steps it so the call it interrupted proceeds normally. tid is known-stopped: it just
// trapped and has not been continued since.
func (tr *tracer) restoreAndStep(tid int, regs *unix.PtraceRegs) error {
	if _, err := unix.PtracePokeText(tid, uintptr(tr.opt.BreakpointAddr), tr.orig); err != nil {
		return err
	}
	tr.armed = false
	regs.SetPC(tr.opt.BreakpointAddr)
	if err := unix.PtraceSetRegs(tid, regs); err != nil {
		return err
	}
	if err := unix.PtraceSingleStep(tid); err != nil {
		return err
	}
	var ws unix.WaitStatus
	_, err := unix.Wait4(tid, &ws, 0, nil)
	return err
}

func (tr *tracer) readMem(tid int, addr uint64, n int) ([]byte, error) {
	if addr == 0 {
		return nil, fmt.Errorf("ptracecap: null address")
	}
	buf := make([]byte, n)
	local := []unix.Iovec{{Base: &buf[0], Len: uint64(n)}}
	remote := []unix.RemoteIovec{{Base: uintptr(addr), Len: n}}
	if cnt, err := unix.ProcessVMReadv(tid, local, remote, 0); err == nil && cnt == n {
		return buf, nil
	}
	if _, err := unix.PtracePeekData(tid, uintptr(addr), buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// candidatesFromRegs returns the byte slices worth validating, per docs/SPEC.md §K.4:
// (a) rdx==32: 32 bytes at rsi;  (b) *(u64)(rsi+16)==32: 32 bytes at *(u64)(rsi+8).
// It is a pure function of regs and the memory reader, so it is unit-testable without ptrace.
func candidatesFromRegs(regs *unix.PtraceRegs, read func(addr uint64, n int) ([]byte, error)) [][]byte {
	var out [][]byte
	if regs.Rdx == 32 {
		if b, err := read(regs.Rsi, 32); err == nil {
			out = append(out, b)
		}
	}
	if hdr, err := read(regs.Rsi, 24); err == nil && len(hdr) == 24 {
		if size := binary.LittleEndian.Uint64(hdr[16:24]); size == 32 {
			ptr := binary.LittleEndian.Uint64(hdr[8:16])
			if b, err := read(ptr, 32); err == nil {
				out = append(out, b)
			}
		}
	}
	return out
}

func (tr *tracer) waitOnce() (Result, bool, error) {
	var ws unix.WaitStatus
	tid, err := unix.Wait4(-1, &ws, unix.WALL|unix.WNOHANG, nil)
	if err != nil {
		if errors.Is(err, unix.ECHILD) {
			time.Sleep(3 * time.Millisecond)
			return Result{}, false, nil
		}
		return Result{}, false, err
	}
	if tid == 0 {
		time.Sleep(3 * time.Millisecond)
		return Result{}, false, nil
	}
	if ws.Exited() || ws.Signaled() {
		delete(tr.attached, tid)
		if tid == tr.pid {
			return Result{}, false, ErrExited
		}
		return Result{}, false, nil
	}
	if !ws.Stopped() {
		return Result{}, false, nil
	}

	if !tr.attached[tid] {
		// A newly cloned/forked thread, auto-stopped by the inherited trace options.
		_ = unix.PtraceSetOptions(tid, ptraceOpts)
		tr.attached[tid] = true
		_ = unix.PtraceCont(tid, 0)
		return Result{}, false, nil
	}

	sig := ws.StopSignal()
	if sig != unix.SIGTRAP {
		_ = unix.PtraceCont(tid, int(sig))
		return Result{}, false, nil
	}
	if event := int((uint32(ws) >> 16) & 0xff); event != 0 {
		if event == unix.PTRACE_EVENT_CLONE || event == unix.PTRACE_EVENT_FORK || event == unix.PTRACE_EVENT_VFORK {
			if msg, err := unix.PtraceGetEventMsg(tid); err == nil {
				tr.attached[int(msg)] = false // will be finished off when its own stop arrives
			}
		}
		_ = unix.PtraceCont(tid, 0)
		return Result{}, false, nil
	}

	var regs unix.PtraceRegs
	if err := unix.PtraceGetRegs(tid, &regs); err != nil {
		return Result{}, false, err
	}
	if regs.PC() != tr.opt.BreakpointAddr+1 {
		_ = unix.PtraceCont(tid, 0)
		return Result{}, false, nil
	}

	// tid just trapped and is still stopped: restore+step (also stopped throughout, via
	// single-step), then either leave it there (accept: no re-arm, just resume) or re-arm the
	// breakpoint *before* resuming it (reject), since once it runs it is no longer safely
	// pokeable without first forcing a stop again.
	for _, cand := range candidatesFromRegs(&regs, func(addr uint64, n int) ([]byte, error) { return tr.readMem(tid, addr, n) }) {
		tr.candidatesSeen++
		if tr.opt.Validate != nil && tr.opt.Validate(cand) {
			if err := tr.restoreAndStep(tid, &regs); err != nil {
				return Result{}, false, err
			}
			_ = unix.PtraceCont(tid, 0)
			return Result{Passphrase: cand, Candidates: tr.candidatesSeen}, true, nil
		}
	}
	if err := tr.restoreAndStep(tid, &regs); err != nil {
		return Result{}, false, err
	}
	if err := tr.arm(tid); err != nil {
		_ = unix.PtraceCont(tid, 0)
		return Result{}, false, err
	}
	_ = unix.PtraceCont(tid, 0)
	return Result{}, false, nil
}

// IsAlive reports whether pid still exists, without requiring it to be a child or tracee of the
// caller (used to verify, after Capture, that the target was actually left running per
// docs/SPEC.md §V.10 — see docs/SPEC.md §B.1 for why this is checked explicitly rather than
// assumed).
func IsAlive(pid int) bool {
	return unix.Kill(pid, 0) == nil
}
