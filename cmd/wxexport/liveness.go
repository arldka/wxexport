package main

import "time"

// verifyAlive polls alive() for up to timeout, sleeping between polls, and reports whether it
// ever observed false (i.e. the target died) during the window. It returns true (healthy) as
// soon as the window elapses with alive() still true throughout the last check, false the
// instant alive() reports false. It is a pure function of the two closures so it is unit
// testable without a real process (see docs/SPEC.md §V.10, §B.1).
func verifyAlive(alive func() bool, timeout, interval time.Duration, sleep func(time.Duration)) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !alive() {
			return false
		}
		if time.Now().After(deadline) {
			return true
		}
		sleep(interval)
	}
}
