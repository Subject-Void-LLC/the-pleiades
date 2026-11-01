// The context a per-task collection child (and a session child) runs in.
package native

import (
	"context"
	"os/signal"
	"syscall"
)

// ChildContext is the context a collection child runs its method in: the
// Runner's own binary and every test binary that stands in for it as a child
// (a TestMain calling RunChildFor) use this one function, so a test's child
// cannot behave differently from the real one. It ends when the Runner sends
// the child SIGTERM, which it does when the task's own context ends (its
// timeout or async limit ran out, or the job was cancelled), before a forced
// kill after childWaitDelay. Ending it cancels the method's work, so a
// command the method started on a device over SSH is stopped there
// (pkg/remoteexec: a signal to a plain command, the stop of a detached one)
// before the child exits, rather than left running when the child dies with
// its connection. A child given a context SIGTERM does not end is killed at
// once by that signal, and stops nothing.
func ChildContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGTERM)
}
