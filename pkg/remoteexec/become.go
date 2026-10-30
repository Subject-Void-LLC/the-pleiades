// Become: running every command a connection sends as another account,
// through sudo, the way Ansible's become does.
package remoteexec

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Become says which account a connection's commands run as. It is sudo
// without a password: sudo is run with -n, so an account that would be asked
// for one fails at once with a message, and no password is ever sent, stored
// or logged by this package.
type Become struct {
	// User is the account to become, root when empty.
	User string
}

// becomeUser is what an account name may be: the characters Linux accounts
// are made of, and nothing sudo could read as an option or a second word.
var becomeUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// target returns the account b names, root when none is.
func (b Become) target() string {
	if b.User == "" {
		return "root"
	}
	return b.User
}

// ValidBecomeUser reports whether name could be a become target.
func ValidBecomeUser(name string) bool { return becomeUser.MatchString(name) }

// WithBecome returns a view of c whose commands run as b's account, after
// proving the login can: one sudo -n true as that account, run unelevated
// first. A sudo that needs a password, or does not allow the account, is
// refused here with what sudo said, so no later command meets the same
// refusal as an exit code a probe might read as an answer (test -w exits 1
// whether the file is unwritable or sudo said no).
//
// The view shares c's connection and, when c was borrowed from a Pool, its
// lease: closing either closes the one connection, and the connection's own
// pool rules still decide whether it is lent on. Run, RunWithStdin,
// RunWithStdinBounded and Start are elevated; Shell, Subsystem, DialTCP and
// DialUnix are refused on the view, since none of them sends a command to
// elevate and each would otherwise run as the login without a word.
func (c *Conn) WithBecome(ctx context.Context, b Become) (*Conn, error) {
	if !becomeUser.MatchString(b.target()) {
		return nil, fmt.Errorf("remoteexec: become user %q is not an account name", b.User)
	}
	if c.become != nil {
		return nil, errors.New("remoteexec: this connection already runs as another account")
	}
	view := *c
	view.become = &b
	res, err := c.run(ctx, view.elevate("true"), nil, 0)
	if err != nil {
		return nil, fmt.Errorf("remoteexec: checking sudo on %s: %w", c.addr, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("remoteexec: this login cannot run commands as %s through sudo without a password on %s (sudo -n exited %d: %s): give the account a NOPASSWD sudo rule, or connect as %s",
			b.target(), c.addr, res.ExitCode, strings.TrimSpace(boundedText(res.Stderr)), b.target())
	}
	return &view, nil
}

// Becomes returns the account the connection's commands run as, or "" when
// they run as the login.
func (c *Conn) Becomes() string {
	if c.become == nil {
		return ""
	}
	return c.become.target()
}

// elevate wraps command to run as the become account, or returns it as it is
// when the connection runs as its login. The whole command is one quoted
// argument to sh -c, so it keeps its pipes and redirects, its standard input
// and its exit status.
func (c *Conn) elevate(command string) string {
	if c.become == nil {
		return command
	}
	return QuoteCommand([]string{"sudo", "-n", "-u", c.become.target(), "--", "sh", "-c", command})
}

// refuseBecome fails a way of using the connection that cannot be elevated.
func (c *Conn) refuseBecome(what string) error {
	if c.become != nil {
		return fmt.Errorf("remoteexec: become does not apply to %s: it sends no command to run as %s, so it would run as the login", what, c.become.target())
	}
	return nil
}

// boundedText cuts text to 200 bytes, for a message about a device's words.
func boundedText(text string) string {
	if len(text) > 200 {
		return text[:200] + "..."
	}
	return text
}
