// A container's recent output, bounded.
package containerengine

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/moby/moby/client"
)

// LogsOps are the operations Logs sends.
var LogsOps = []Op{OpContainerLogs}

// Logs returns the last tail lines container wrote to standard output and
// standard error, refused rather than cut past limit bytes together. tty
// says the container runs with a terminal, whose output the engine sends
// as one stream rather than framed; since is an engine timestamp or
// duration, "" for no lower bound.
func (s *Session) Logs(ctx context.Context, container string, tail int, since string, tty bool, limit int) (string, string, error) {
	if err := ValidName("container", container); err != nil {
		return "", "", err
	}
	rd, err := s.API.ContainerLogs(ctx, container, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: strconv.Itoa(tail), Since: since,
	})
	if err != nil {
		return "", "", fmt.Errorf("reading container %s's logs: %w", container, err)
	}
	defer func() { _ = rd.Close() }()
	if tty {
		data, err := io.ReadAll(io.LimitReader(rd, int64(limit)+1))
		if err != nil {
			return "", "", fmt.Errorf("reading container %s's logs: %w", container, err)
		}
		if len(data) > limit {
			return "", "", fmt.Errorf("container %s's logs are over %d bytes; ask for fewer lines", container, limit)
		}
		return string(data), "", nil
	}
	stdout, stderr, err := demux(rd, limit)
	if err != nil {
		return "", "", fmt.Errorf("reading container %s's logs: %w", container, err)
	}
	return string(stdout), string(stderr), nil
}
