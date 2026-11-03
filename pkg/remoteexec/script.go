// Sending every command so that sh, not the login shell, reads it
// (pkg/remoteexec/shline), unless the connection is Raw.
package remoteexec

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/shline"
)

// wire is what c sends for command: elevated when c runs as another account,
// then, unless c is Raw, encoded so sh reads it whatever the login shell is.
// A line longer than shline.MaxBytes is refused before anything is sent.
func (c *Conn) wire(command string) (string, error) {
	line := c.elevate(command)
	if !c.raw {
		line = shline.For(line)
	}
	if len(line) > shline.MaxBytes {
		return "", fmt.Errorf("remoteexec: the command for %s is %d bytes as sent, over the %d one argument may hold; send its data on standard input", c.addr, len(line), shline.MaxBytes)
	}
	return line, nil
}

// Raw returns a view of c that sends each command as it is written, for the
// login shell to read: for a device whose exec channel is not a POSIX shell (a
// network device's CLI, a Windows OpenSSH server's cmd.exe), where sh -c is not
// a command. It shares c's connection and lease, as WithBecome's view does.
func (c *Conn) Raw() *Conn {
	view := *c
	view.raw = true
	return &view
}
