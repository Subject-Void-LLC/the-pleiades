// Package cmlconsole reaches a Cisco Modeling Labs node through its
// controller's console server: attached to the node's line by the line's
// console UUID ("connect <uuid>", which names exactly one node, where a lab
// title in "open /<title>/..." may name two), the node's own login answered
// by matching its prompts, never by waiting for silence, and the line
// logged out on close, so the next person on that console never inherits a
// privileged prompt.
//
// It works over any netcli.Shell, so the SSH session to the console server
// is the caller's (pkg/sdk opens it with the server's own credential and
// verified host key). Every step is bounded by its context and a byte
// limit.
package cmlconsole

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
)

// maxBytes bounds one step's read: a node booting prints its whole boot
// log before its first prompt.
const maxBytes = 1 << 20

// maxLoginSteps bounds the login dialog: Press RETURN, a username, a
// password, and leaving a configuration mode are each a step.
const maxLoginSteps = 6

// logoutWait bounds the logout a Close sends, on its own context, since it
// runs after the task's own may have ended.
const logoutWait = 10 * time.Second

// lineUUID is what a console line must be before it is written to the
// console server's prompt.
var lineUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var (
	// serverPrompt is the console server's prompt at the end of what was
	// read; it redraws itself with ANSI sequences on every keystroke.
	serverPrompt = regexp.MustCompile(`consoles> \z`)
	// connected ends a connect: the line is attached, or the server said
	// why not and drew its prompt again after the command's newline.
	connected = regexp.MustCompile(`(?s)\r\n.*?(Escape character is '\^\]'\.\r?\n|consoles> \z)`)
	// nodeTurn is the node asking for something, or its prompt, at the end
	// of what was read.
	nodeTurn = regexp.MustCompile(`(?i)(username:\s*|password:\s*|press return to get started[.!]?\s*|\S+?(\([^)\s]*\))?[>#]\s*)\z`)
	// configPrompt is a prompt inside a configuration mode.
	configPrompt = regexp.MustCompile(`\(\S*config\S*\)#\s*\z`)
	// ansi matches the terminal control sequences the server draws with.
	ansi = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")
)

// Login is a node's own account.
type Login struct {
	Username, Password string
}

// Attach waits for the console server's prompt on shell, then connects to
// the console line, a UUID, refusing anything else before it reaches the
// prompt.
func Attach(ctx context.Context, shell netcli.Shell, line string) error {
	if !lineUUID.MatchString(line) {
		return fmt.Errorf("cmlconsole: console line %q is not a console UUID", line)
	}
	if _, err := shell.ReadUntil(ctx, serverPrompt, maxBytes); err != nil {
		return fmt.Errorf("cmlconsole: waiting for the console server's prompt: %w", err)
	}
	if err := shell.WriteLine(ctx, "connect "+line); err != nil {
		return err
	}
	out, err := shell.ReadUntil(ctx, connected, maxBytes)
	if err != nil {
		return fmt.Errorf("cmlconsole: connecting to console %s: %w", line, err)
	}
	if !strings.Contains(out, "Escape character is") {
		return fmt.Errorf("cmlconsole: the console server did not connect console %s: %s", line, lastLine(serverPrompt.ReplaceAllString(ansi.ReplaceAllString(out, ""), "")))
	}
	return nil
}

// LogIn answers the node's login on an attached line: it presses return,
// then answers each prompt once (a username or password asked twice is a
// refused login), leaves any configuration mode, and stops at the node's
// exec prompt, which it asks for again so the session that reads next
// (netcli.Open) finds one waiting. login may be empty for a line with no
// login of its own.
func LogIn(ctx context.Context, shell netcli.Shell, login Login) error {
	if err := shell.WriteLine(ctx, ""); err != nil {
		return err
	}
	var sentUser, sentPass bool
	for step := 0; step < maxLoginSteps; step++ {
		out, err := shell.ReadUntil(ctx, nodeTurn, maxBytes)
		if err != nil {
			return fmt.Errorf("cmlconsole: waiting for the node's login: %w", err)
		}
		last := strings.ToLower(lastLine(out))
		var answer string
		switch {
		case strings.HasPrefix(last, "username"):
			if sentUser || login.Username == "" {
				return errors.New("cmlconsole: the node refused the login (it asked for a username again)")
			}
			answer, sentUser = login.Username, true
		case strings.HasPrefix(last, "password"):
			if sentPass || login.Password == "" {
				return errors.New("cmlconsole: the node refused the login (it asked for a password again)")
			}
			answer, sentPass = login.Password, true
		case strings.HasPrefix(last, "press return"):
			answer = ""
		case configPrompt.MatchString(lastLine(out)):
			answer = "end"
		default:
			// The exec prompt: ask for it again for the next reader.
			return shell.WriteLine(ctx, "")
		}
		if err := shell.WriteLine(ctx, answer); err != nil {
			return err
		}
	}
	return fmt.Errorf("cmlconsole: the node's login took more than %d steps", maxLoginSteps)
}

// LogOut ends the node's exec session on the line, leaving any mode first,
// on a context of its own, so the next person on that console meets a
// login and not this session's privileged prompt. A line already gone is
// not an error: there is nothing left to log out.
func LogOut(shell netcli.Shell) error {
	ctx, cancel := context.WithTimeout(context.Background(), logoutWait)
	defer cancel()
	if err := shell.WriteLine(ctx, "end"); err != nil {
		return nil
	}
	if _, err := shell.ReadUntil(ctx, nodeTurn, maxBytes); err != nil {
		return nil
	}
	_ = shell.WriteLine(ctx, "exit")
	return nil
}

// lastLine is the last non-empty line of s, terminal sequences taken out,
// at most 80 bytes, for a message.
func lastLine(s string) string {
	s = ansi.ReplaceAllString(s, "")
	lines := strings.FieldsFunc(s, func(r rune) bool { return r == '\r' || r == '\n' })
	if len(lines) == 0 {
		return ""
	}
	l := strings.TrimSpace(lines[len(lines)-1])
	if len(l) > 80 {
		l = l[:80]
	}
	return l
}
