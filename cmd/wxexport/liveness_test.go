package main

import "time"

import "testing"

func TestVerifyAliveStaysUpThroughoutWindow(t *testing.T) {
	calls := 0
	alive := func() bool { calls++; return true }
	fakeNow := time.Now()
	slept := 0
	sleep := func(time.Duration) { slept++; fakeNow = fakeNow.Add(50 * time.Millisecond) }
	if !verifyAlive(alive, 120*time.Millisecond, 50*time.Millisecond, sleep) {
		t.Error("expected true: alive() never returned false")
	}
	if calls < 2 || slept < 2 {
		t.Errorf("calls=%d slept=%d, expected several polls", calls, slept)
	}
}

func TestVerifyAliveDetectsDeath(t *testing.T) {
	calls := 0
	alive := func() bool {
		calls++
		return calls < 3 // dies on the 3rd poll
	}
	sleep := func(time.Duration) {}
	if verifyAlive(alive, time.Second, time.Millisecond, sleep) {
		t.Error("expected false: alive() reported death")
	}
	if calls != 3 {
		t.Errorf("calls=%d, want exactly 3 (stops at the first false)", calls)
	}
}

func TestVerifyAliveDeadImmediately(t *testing.T) {
	alive := func() bool { return false }
	if verifyAlive(alive, time.Second, time.Millisecond, func(time.Duration) {}) {
		t.Error("expected false")
	}
}
