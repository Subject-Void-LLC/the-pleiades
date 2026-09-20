// Package loader's tests. This file holds TestMain, which runs the whole
// suite under goleak: every run starts goroutines (os/exec's copiers, the
// response channel's reader), and a test that leaves one behind fails the
// package rather than passing with a leak.
package loader

import (
	"os"
	"testing"

	"go.uber.org/goleak"
)

// afterAll holds cleanup work that must run once, after every test, such
// as removing the directory the real Go fixture program is built into.
var afterAll []func()

// testProbes are special runs of this test binary that a test starts as
// a subprocess, each registered by an init function only when its
// environment variable is set. The first one registered replaces the
// whole test run.
var testProbes []func() int

// TestMain runs every test, runs afterAll, and then fails the run if any
// goroutine a test started is still alive. In a probe run it runs the
// probe instead (testProbes).
func TestMain(m *testing.M) {
	if len(testProbes) > 0 {
		os.Exit(testProbes[0]())
	}
	goleak.VerifyTestMain(m, goleak.Cleanup(func(code int) {
		for _, f := range afterAll {
			f()
		}
		os.Exit(code)
	}))
}
