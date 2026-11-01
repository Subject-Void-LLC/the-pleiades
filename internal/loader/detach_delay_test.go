//go:build unix

// Tests that a program running a detached command is given the time its
// stop on the device takes before the loader kills it.
package loader

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// TestNewCommand_ADetachedRunWaitsOutTheStop: the kill after SIGTERM waits
// remoteexec.DetachStopBudget longer under a detached context, and only
// there, so the program's stop of its detached command reaches the device
// before the program is killed.
func TestNewCommand_ADetachedRunWaitsOutTheStop(t *testing.T) {
	o := testOptions()
	path := filepath.Join(t.TempDir(), "prog")
	sb, err := newSandbox(path, o)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.close()
	plain := newCommand(context.Background(), path, "invoke", nil, o, sb)
	if plain.WaitDelay != o.waitDelay {
		t.Errorf("a plain run's kill delay = %s; want the grace alone, %s", plain.WaitDelay, o.waitDelay)
	}
	detached := newCommand(remoteexec.WithDetach(context.Background()), path, "invoke", nil, o, sb)
	if want := o.waitDelay + remoteexec.DetachStopBudget; detached.WaitDelay != want {
		t.Errorf("a detached run's kill delay = %s; want the grace plus the stop budget, %s", detached.WaitDelay, want)
	}
}
