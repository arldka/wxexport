//go:build linux

package ptracecap

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/arldka/wxexport/internal/procfs"
)

// ---- pure unit tests: no process, no ptrace ----

func TestCandidatesFromRegsFormA(t *testing.T) {
	want := bytes.Repeat([]byte{7}, 32)
	regs := &unix.PtraceRegs{Rsi: 0x1000, Rdx: 32}
	got := candidatesFromRegs(regs, func(addr uint64, n int) ([]byte, error) {
		if addr == 0x1000 && n == 32 {
			return want, nil
		}
		return nil, fmt.Errorf("unexpected read addr=%x n=%d", addr, n)
	})
	if len(got) != 1 || !bytes.Equal(got[0], want) {
		t.Fatalf("got %v", got)
	}
}

func TestCandidatesFromRegsFormB(t *testing.T) {
	want := bytes.Repeat([]byte{8}, 32)
	hdr := make([]byte, 24)
	binary.LittleEndian.PutUint64(hdr[8:], 0x2000) // data ptr at +8
	binary.LittleEndian.PutUint64(hdr[16:], 32)    // size at +16
	regs := &unix.PtraceRegs{Rsi: 0x1000, Rdx: 99} // rdx != 32: form (a) must not fire
	got := candidatesFromRegs(regs, func(addr uint64, n int) ([]byte, error) {
		switch {
		case addr == 0x1000 && n == 24:
			return hdr, nil
		case addr == 0x2000 && n == 32:
			return want, nil
		}
		return nil, fmt.Errorf("unexpected read addr=%x n=%d", addr, n)
	})
	if len(got) != 1 || !bytes.Equal(got[0], want) {
		t.Fatalf("got %v", got)
	}
}

func TestCandidatesFromRegsBothShapes(t *testing.T) {
	a := bytes.Repeat([]byte{1}, 32)
	b := bytes.Repeat([]byte{2}, 32)
	hdr := make([]byte, 24)
	binary.LittleEndian.PutUint64(hdr[8:], 0x2000)
	binary.LittleEndian.PutUint64(hdr[16:], 32)
	regs := &unix.PtraceRegs{Rsi: 0x1000, Rdx: 32}
	got := candidatesFromRegs(regs, func(addr uint64, n int) ([]byte, error) {
		switch {
		case addr == 0x1000 && n == 32:
			return a, nil
		case addr == 0x1000 && n == 24:
			return hdr, nil
		case addr == 0x2000 && n == 32:
			return b, nil
		}
		return nil, fmt.Errorf("unexpected addr=%x n=%d", addr, n)
	})
	if len(got) != 2 || !bytes.Equal(got[0], a) || !bytes.Equal(got[1], b) {
		t.Fatalf("got %v", got)
	}
}

func TestCandidatesFromRegsNoMatch(t *testing.T) {
	regs := &unix.PtraceRegs{Rsi: 0x1000, Rdx: 4}
	got := candidatesFromRegs(regs, func(addr uint64, n int) ([]byte, error) {
		if n == 24 {
			return make([]byte, 24), nil // size field is 0
		}
		return nil, errors.New("should not be read")
	})
	if len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}

// ---- integration test: real ptrace against a built fixture binary ----

func buildTestTarget(t *testing.T) (path string, hookVA uint64) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available to build the test target")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "testtarget")
	cmd := exec.Command("go", "build", "-o", bin, "./testtarget")
	cmd.Dir = "." // this package's directory, which contains ./testtarget
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build test target: %v\n%s", err, out)
	}
	f, err := elf.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	syms, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range syms {
		if s.Name == "main.probeHook" || s.Name == "main.probeHook.abi0" {
			return bin, s.Value
		}
	}
	t.Fatal("main.probeHook symbol not found; was the binary stripped?")
	return "", 0
}

// staticToRuntime resolves hookVA (a static ELF vaddr) to its runtime address. `go build`
// defaults to a non-PIE EXEC on linux/amd64 (verified: `file` reports "ELF ... executable",
// readelf -h reports Type EXEC), so the static vaddr already equals the runtime address; this
// is checked against the live process's maps rather than assumed, using the same procfs.Maps
// parser production code relies on.
func staticToRuntime(t *testing.T, bin string, pid int, staticVA uint64) uint64 {
	t.Helper()
	maps, err := procfs.Default().Maps(pid)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range maps {
		if m.Path == abs && staticVA >= m.Start && staticVA < m.End {
			return staticVA // non-PIE: no relocation
		}
	}
	t.Fatalf("static address %#x of %s not covered by any mapping in pid %d", staticVA, abs, pid)
	return 0
}

// killAndWaitBounded avoids the whole test suite hanging for its full -timeout if cleanup
// itself gets stuck (e.g. on a lingering, unexpected ptrace relationship): it reports via
// t.Logf instead of blocking forever.
func killAndWaitBounded(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	cmd.Process.Kill()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		t.Logf("cmd.Wait() returned: %v", err)
	case <-time.After(3 * time.Second):
		t.Logf("cmd.Wait() did not return within 3s after Kill (pid %d) -- leaking a goroutine/process", cmd.Process.Pid)
	}
}

func TestCaptureEndToEnd(t *testing.T) {
	bin, hookVA := buildTestTarget(t)
	cmd := exec.Command(bin)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	defer killAndWaitBounded(t, cmd)
	time.Sleep(50 * time.Millisecond) // let it map and start looping

	runtimeAddr := staticToRuntime(t, bin, pid, hookVA)

	wantBuf := []byte{
		0xaa, 0xbb, 0xcc, 0xdd, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12,
		13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 0xee,
	}

	t.Run("rejects then accepts", func(t *testing.T) {
		calls := 0
		validate := func(b []byte) bool {
			calls++
			return calls >= 2 && bytes.Equal(b, wantBuf) // force at least one reject+rearm cycle
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res, err := Capture(ctx, pid, Options{BreakpointAddr: runtimeAddr, Timeout: 10 * time.Second, Validate: validate})
		if err != nil {
			t.Fatalf("Capture: %v", err)
		}
		if !bytes.Equal(res.Passphrase, wantBuf) {
			t.Fatalf("passphrase = % x, want % x", res.Passphrase, wantBuf)
		}
		if calls < 2 {
			t.Fatalf("expected at least 2 calls (reject then accept), got %d", calls)
		}
	})

	t.Run("target still running and responsive after capture", func(t *testing.T) {
		// A second capture must still be able to attach and catch another hit, proving the
		// first Capture detached cleanly and the breakpoint byte was fully restored.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res, err := Capture(ctx, pid, Options{BreakpointAddr: runtimeAddr, Timeout: 10 * time.Second,
			Validate: func(b []byte) bool { return bytes.Equal(b, wantBuf) }})
		if err != nil {
			t.Fatalf("second Capture: %v", err)
		}
		if !bytes.Equal(res.Passphrase, wantBuf) {
			t.Fatalf("passphrase = % x", res.Passphrase)
		}
	})
}

func TestCaptureTimeoutLeavesTargetRunningAndDetached(t *testing.T) {
	bin, hookVA := buildTestTarget(t)
	cmd := exec.Command(bin)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	defer killAndWaitBounded(t, cmd)
	time.Sleep(50 * time.Millisecond)
	runtimeAddr := staticToRuntime(t, bin, pid, hookVA)

	ctx := context.Background()
	_, err := Capture(ctx, pid, Options{BreakpointAddr: runtimeAddr, Timeout: 200 * time.Millisecond,
		Validate: func([]byte) bool { return false }})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("got %v, want ErrTimeout", err)
	}
	// After a clean detach, the target must still be alive and schedulable.
	time.Sleep(50 * time.Millisecond)
	if err := cmd.Process.Signal(unix.Signal(0)); err != nil {
		t.Fatalf("target not running after timeout+detach: %v", err)
	}
}

func TestCaptureBadPidErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Capture(ctx, 999999, Options{BreakpointAddr: 1, Timeout: time.Second})
	if err == nil {
		t.Fatal("expected an error for a nonexistent pid")
	}
}

func TestIsAlive(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Skip("no `sleep` binary available")
	}
	pid := cmd.Process.Pid
	if !IsAlive(pid) {
		t.Error("IsAlive(running pid) = false")
	}
	cmd.Process.Kill()
	cmd.Wait()
	// Reaped (not a zombie, since we called Wait): the pid should no longer be reported alive.
	if IsAlive(pid) {
		t.Error("IsAlive(reaped pid) = true")
	}
}
