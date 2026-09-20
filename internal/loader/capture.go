// Package loader: the capped buffer that keeps a program's output
// bounded, and the scrubbed environment a program is started with.
package loader

import (
	"bytes"
	"os"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// cappedBuffer keeps the first limit bytes written to it and discards the
// rest, while still reporting every write as complete.
//
// Reporting the discarded bytes as written is the point. os/exec copies a
// child's stdout into this writer from a goroutine of its own, and a
// writer that returned an error or blocked once full would stop that
// copy, fill the pipe, and leave the child stuck writing to it until the
// timeout. Swallowing the excess keeps the pipe draining, so a program
// that floods its output costs only the time it takes to read it.
type cappedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int64
	discarded int64
}

// newCappedBuffer returns a cappedBuffer keeping at most limit bytes.
func newCappedBuffer(limit int64) *cappedBuffer {
	return &cappedBuffer{limit: limit}
}

// Write keeps what fits under the cap, counts the rest as discarded, and
// always reports len(p) written with no error.
func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	room := c.limit - int64(c.buf.Len())
	switch {
	case room <= 0:
		c.discarded += int64(len(p))
	case int64(len(p)) <= room:
		c.buf.Write(p)
	default:
		c.buf.Write(p[:room])
		c.discarded += int64(len(p)) - room
	}
	return len(p), nil
}

// Bytes returns a copy of what was kept.
func (c *cappedBuffer) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return bytes.Clone(c.buf.Bytes())
}

// String returns what was kept.
func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// Truncated reports whether anything was discarded.
func (c *cappedBuffer) Truncated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.discarded > 0
}

// allowedEnv is every environment variable a program may inherit, and
// the reason each one is here:
//
//   - PATH, so a program can find the tools it shells out to.
//   - HOME, because pkg/remoteexec's default known_hosts file lives there.
//   - TMPDIR, so scratch files land where the operator wants them.
//   - LANG, LC_ALL and TZ, so text and times come out the way the rest of
//     the process sees them.
//   - remoteexec.KnownHostsEnv, the deployment's chosen known_hosts file,
//     without which a program's SSH connection could not verify a host
//     the parent itself would accept.
//
// Nothing else is handed to the program. That decides what the program
// inherits, not what it can reach: running as the same user, it could
// read the parent's starting environment (the master key, the broker's
// credentials, API tokens) from /proc/<parent pid>/environ, and what
// stops that is confinement and protectProcess, not this list
// (FAILURE_PATTERNS 251). SSH_AUTH_SOCK
// is left out on purpose: a program is handed the credential the
// credential manager resolved for its task, and an agent socket would
// hand it every key the user holds.
var allowedEnv = []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "TZ", remoteexec.KnownHostsEnv}

// scrubbedEnv returns the environment a program runs with: each allowed
// variable that is set in this process, and nothing else.
//
// The result is never nil. os/exec reads a nil Cmd.Env as "inherit
// everything", which is exactly what this function exists to prevent, so
// an empty environment must be an empty slice.
func scrubbedEnv() []string {
	env := make([]string, 0, len(allowedEnv))
	for _, key := range allowedEnv {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}
