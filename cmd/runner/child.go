// The context a per-task collection child (and a session child) runs in.
package main

import (
	"context"
	"os/signal"
	"syscall"
)

// childContext is the context a collection child runs its method in. It
// ends when the Runner sends the child SIGTERM, which it does when the
// task's own context ends (its timeout ran out, or the job was cancelled),
// before a forced kill after native's childWaitDelay. Ending it cancels the
// method's work, so a command the method started on a device over SSH is
// sent SIGTERM there (pkg/remoteexec) before the child exits, rather than
// left running when the child dies with its connection.
func childContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGTERM)
}
