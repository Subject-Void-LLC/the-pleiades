//go:build !windows

// Tests for the collection child's context, which a SIGTERM from the Runner
// ends. Windows delivers no SIGTERM to a process, and a Runner runs on
// Linux.
package main

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// TestChildContext_EndsOnSIGTERM: the SIGTERM the Runner sends a child whose
// task's context ended cancels the method's context, rather than killing
// the child before the method can stop what it started on the device.
func TestChildContext_EndsOnSIGTERM(t *testing.T) {
	ctx, stop := childContext()
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM did not end the child's context")
	}
}
