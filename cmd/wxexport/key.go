package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/arldka/wxexport/internal/discover"
	"github.com/arldka/wxexport/internal/elfhook"
	"github.com/arldka/wxexport/internal/keys"
	"github.com/arldka/wxexport/internal/procfs"
	"github.com/arldka/wxexport/internal/ptracecap"
)

func cmdKey(ctx context.Context, g globalFlags, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: wxexport key capture|set|check|forget")
	}
	switch args[0] {
	case "check":
		return keyCheck(g)
	case "set":
		return keySet(g, args[1:])
	case "forget":
		return keyForget(g)
	case "capture":
		return keyCapture(ctx, g, args[1:])
	}
	return fmt.Errorf("unknown key subcommand %q", args[0])
}

func keyCheck(g globalFlags) error {
	e, err := g.load()
	if err != nil {
		return err
	}
	c, inv, err := loadKeys(e)
	if err != nil {
		return err
	}
	ks, res, rerr := keys.Resolve(c, inv)
	bad := 0
	for _, r := range res {
		fmt.Printf("%-12s %s\n", r.Status, r.DB.Rel)
		if r.Status == keys.StatusMissing || r.Status == keys.StatusError {
			bad++
		}
	}
	if rerr != nil {
		return rerr
	}
	if len(c.DBs) > 0 {
		c.Account = e.account.Name
		if err := c.Save(e.keysPath); err != nil {
			return err
		}
	}
	fmt.Printf("\n%d/%d databases keyed\n", len(ks), len(res))
	if bad > 0 {
		return fmt.Errorf("%d database(s) could not be keyed", bad)
	}
	return nil
}

func keySet(g globalFlags, args []string) error {
	fs := flag.NewFlagSet("key set", flag.ContinueOnError)
	pass := fs.String("passphrase", "", "64 hex characters (32 bytes)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	b, err := hex.DecodeString(*pass)
	if err != nil || len(b) != 32 {
		return errors.New("--passphrase must be exactly 64 hex characters")
	}
	e, err := g.load()
	if err != nil {
		return err
	}
	c, inv, err := loadKeys(e)
	if err != nil {
		return err
	}
	probe := *c
	probe.PassphraseHex, probe.DBs = *pass, nil
	if _, _, err := keys.Resolve(&probe, inv); err != nil {
		return errors.New("passphrase does not validate against any database; not saved")
	}
	probe.Account, probe.CapturedAt = e.account.Name, time.Now().UTC()
	if err := probe.Save(e.keysPath); err != nil {
		return err
	}
	fmt.Printf("passphrase validated and saved to %s\n", e.keysPath)
	return nil
}

func keyForget(g globalFlags) error {
	e, err := g.load()
	if err != nil {
		return err
	}
	if err := os.Remove(e.keysPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Println("key cache removed")
	return nil
}

// keyCapture implements wxexport key capture (SPEC.md §K.1-5): it relaunches WeChat as its own
// child (so ptrace is allowed under kernel.yama.ptrace_scope=1 with no sudo), locates the WCDB
// cipher-config hook by static analysis, and runs ptracecap.Capture against it, validating every
// candidate against the account's own databases before accepting it.
func keyCapture(ctx context.Context, g globalFlags, args []string) error {
	fs := flag.NewFlagSet("key capture", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 180*time.Second, "how long to wait for the breakpoint to be hit")
	kill := fs.Bool("kill", false, "automatically stop a currently running WeChat before relaunching it")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "NOTICE: key capture attaches a debugger (ptrace) to WeChat. Proof of concept, use at your own risk.\n"+
		"        Use it ONLY on your own account, on your own machine, with your own login.")

	e, err := g.load()
	if err != nil {
		return err
	}
	c, inv, err := loadKeys(e)
	if err != nil {
		return err
	}
	core := discover.CoreDBs(inv)
	if len(core) == 0 {
		return fmt.Errorf("no databases found under %s", e.account.DBStorage)
	}
	probe := core[0]
	probePage1, err := readFirstPage(probe.Path)
	if err != nil {
		return fmt.Errorf("read %s: %w", probe.Rel, err)
	}

	in, err := discover.FindInstall(discover.ExecRunner)
	if err != nil {
		return err
	}
	fmt.Printf("WeChat %s at %s\n", in.Version, in.BinaryPath)

	hook, err := elfhook.FindHook(in.BinaryPath)
	if err != nil {
		return fmt.Errorf("could not locate the WCDB cipher hook in this WeChat version (%s): %w"+
			"\nthis WeChat version may not be supported; you can still use `wxexport key set --passphrase ...`"+
			" if you obtain the passphrase another way", in.Version, err)
	}
	binHash, err := sha256File(in.BinaryPath)
	if err != nil {
		return err
	}

	pfs := procfs.Default()
	if running := discover.RunningPids(pfs); len(running) > 0 {
		if !*kill {
			return fmt.Errorf("WeChat is already running (pid(s) %v); capture needs to relaunch it as its "+
				"own child (ptrace only works on descendants here). Close WeChat yourself and re-run, or "+
				"pass --kill to let wxexport stop it with `flatpak kill %s`", running, discover.FlatpakID)
		}
		fmt.Printf("stopping running WeChat (pid(s) %v)...\n", running)
		if out, err := exec.Command("flatpak", "kill", discover.FlatpakID).CombinedOutput(); err != nil {
			return fmt.Errorf("flatpak kill: %w: %s", err, out)
		}
		deadline := time.Now().Add(10 * time.Second)
		for len(discover.RunningPids(pfs)) > 0 && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
	}

	fmt.Println("launching WeChat...")
	launcher := exec.Command("flatpak", "run", discover.FlatpakID)
	launcher.Stdout, launcher.Stderr = os.Stdout, os.Stderr
	if err := launcher.Start(); err != nil {
		return fmt.Errorf("flatpak run %s: %w", discover.FlatpakID, err)
	}
	launcherPid := launcher.Process.Pid

	fmt.Println("waiting for the WeChat process to start...")
	pid, err := waitForDescendant(ctx, pfs, launcherPid, discover.SandboxBinary, 30*time.Second)
	if err != nil {
		return err
	}
	fmt.Printf("found WeChat pid %d; computing breakpoint address...\n", pid)

	bpAddr, err := waitForBreakpointAddr(pfs, pid, hook, 10*time.Second)
	if err != nil {
		return err
	}

	fmt.Println("waiting for you to click \"Log in\" in WeChat (this derives the key once, from your own already-authenticated session)...")
	captureCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	res, err := ptracecap.Capture(captureCtx, pid, ptracecap.Options{
		BreakpointAddr: bpAddr,
		Timeout:        *timeout,
		Validate: func(cand []byte) bool {
			_, ok := keys.TryPassphrase(cand, probePage1)
			return ok
		},
	})
	if err != nil {
		if errors.Is(err, ptracecap.ErrTimeout) {
			return fmt.Errorf("timed out after %s waiting for the login breakpoint; make sure you actually "+
				"logged in during capture, then try again", *timeout)
		}
		return fmt.Errorf("capture failed: %w", err)
	}
	fmt.Printf("captured passphrase after %d candidate(s)\n", res.Candidates)

	c.Account = e.account.Name
	c.WeChatVersion = in.Version
	c.BinarySHA256 = binHash
	c.HookOffset = hook.HookVA
	c.PassphraseHex = hex.EncodeToString(res.Passphrase)
	c.CapturedAt = time.Now().UTC()
	c.DBs = nil // force re-derivation against the freshly captured passphrase

	ks, dres, rerr := keys.Resolve(c, inv)
	for _, r := range dres {
		fmt.Printf("%-12s %s\n", r.Status, r.DB.Rel)
	}
	if err := c.Save(e.keysPath); err != nil {
		return err
	}
	fmt.Printf("\nsaved to %s: %d/%d databases keyed\n", e.keysPath, len(ks), len(dres))

	// SPEC.md §V.10 / §B.1: a real WeChat 4.1.13 instance has been observed to exit on its own a
	// few seconds after a successful capture+detach (cause unconfirmed: possibly an anti-debug
	// self-check, possibly a side effect of our own ptrace sequence). The key above is already
	// valid and saved either way, but we must not silently claim WeChat was "left running" (the
	// spec's own invariant) without checking, so this is verified explicitly and reported as a
	// distinct, clearly-worded condition rather than folded into the exit code of the capture
	// itself.
	if !verifyAlive(func() bool { return ptracecap.IsAlive(pid) }, 10*time.Second, 300*time.Millisecond, time.Sleep) {
		fmt.Fprintln(os.Stderr, "\nWARNING: the key above was captured and saved successfully, but WeChat exited on"+
			" its own shortly after (see docs/SPEC.md §B.1 — this is a known, not-yet-fixed issue, possibly"+
			" anti-debug self-protection). Please relaunch WeChat yourself (`flatpak run "+discover.FlatpakID+"`).")
		return errors.New("WeChat did not stay running after capture (key was still saved; see warning above)")
	}

	if rerr != nil {
		return rerr
	}
	return nil
}

func readFirstPage(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, 4096)
	if _, err := io.ReadFull(f, b); err != nil {
		return nil, err
	}
	return b, nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// waitForDescendant polls for a process whose exe link matches exePath and which descends from
// ancestorPid (the launcher we started), returning its pid.
func waitForDescendant(ctx context.Context, fs procfs.FS, ancestorPid int, exePath string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		for _, pid := range fs.FindByExe(exePath) {
			if fs.Descends(pid, ancestorPid) {
				return pid, nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return 0, fmt.Errorf("timed out waiting for %s to start", exePath)
}

// waitForBreakpointAddr resolves the hook's static address to a runtime address once the binary
// is mapped into pid (the mapping may not exist for a few milliseconds right after exec).
func waitForBreakpointAddr(fs procfs.FS, pid int, hook elfhook.Result, timeout time.Duration) (uint64, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		maps, err := fs.Maps(pid)
		if err == nil {
			if base, ok := procfs.ImageBase(maps, discover.SandboxBinary); ok {
				return base + hook.HookVA - hook.FirstLoadVaddr, nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return 0, fmt.Errorf("timed out waiting for %s to be mapped into pid %d", discover.SandboxBinary, pid)
}
