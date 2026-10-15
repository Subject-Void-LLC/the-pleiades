// How long each docker call may take, so a daemon that stops answering
// cannot hold a task, and the device lock it holds, forever.
package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// Deadlines for one docker call each. The command line sets no task timeout
// at all and a template's defaults to none, so without these a daemon that
// accepts a connection and never answers held the run indefinitely. Each is
// generous for a daemon that is working: inspect and info answer in
// milliseconds, docker stop waits its own ten second grace before killing,
// and creating or starting a container whose image the engine holds takes
// seconds. Variables so a test can shorten them (export_test.go).
var (
	quickCallTimeout  = 30 * time.Second
	startCallTimeout  = 2 * time.Minute
	stopCallTimeout   = 90 * time.Second
	removeCallTimeout = 60 * time.Second
)

// paramPullTimeout is container.docker.run's bound on pulling an image, in
// seconds: the one call whose honest duration depends on the image's size
// and the link, so the task may declare it.
const paramPullTimeout = "pull_timeout"

// pullTimeoutDefault is a pull's bound when the task declares none.
const pullTimeoutDefault = 15 * time.Minute

// runBounded runs command on conn within d, its output bounded
// (dockerOutputLimit). When it is the deadline, and not the task's own
// context, that ended the call, the error says so, and that a change the
// call was making may or may not have happened: the session is closed under
// the remote command, which the engine may finish anyway.
func runBounded(ctx context.Context, conn *remoteexec.Conn, command string, stdin io.Reader, d time.Duration) (remoteexec.Result, error) {
	callCtx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	result, err := conn.RunWithStdinBounded(callCtx, command, stdin, dockerOutputLimit)
	if err != nil && ctx.Err() == nil && errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		return result, fmt.Errorf("%s did not answer within %s, so a change it was making may or may not have happened: %w",
			remoteexec.Excerpt(command, 200), d, err)
	}
	return result, err
}
