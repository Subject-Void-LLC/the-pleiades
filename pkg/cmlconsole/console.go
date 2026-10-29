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
// verified host key). Every read is bounded by its context, a byte limit
// and a time limit of its own, since a run sets no deadline of its own by
// default and a console server or a line that never answers would
// otherwise hold the run forever.
package cmlconsole

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
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

var (
	// serverWait bounds each read of the console server: its prompt, and
	// its answer to a connect.
	serverWait = 30 * time.Second
	// nodeWait bounds each read of the node's login: long enough for a
	// node still printing its boot log to reach its prompt.
	nodeWait = 3 * time.Minute
)

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

// ErrRefused is what LogIn's error wraps when the node refused the login,
// or asked for a part of it the device's credential does not hold. The line
// is then at a login prompt, where there is no session to log out, and
// anything typed would be one more failed login.
var ErrRefused = errors.New("the login was refused")

// refused is a login refusal saying why, which errors.Is finds as
// ErrRefused.
type refused string

func (r refused) Error() string        { return "cmlconsole: " + string(r) }
func (r refused) Is(target error) bool { return target == ErrRefused }

// Questions a node's line can be left on whose decline is certain on IOS,
// and the decline: at "[confirm]" any key but Enter or y declines, and
// "[yes/no]:" takes "no" (both measured on IOL XE 17.15 through
// net.ios.command and net.ios.config, 2026-10-24: clear counters and crypto
// key zeroize, each back at its prompt with nothing done).
var (
	confirmQuestion = regexp.MustCompile(`(?i)\[confirm\][ \t]*\z`)
	yesNoQuestion   = regexp.MustCompile(`(?i)\[yes/no\]:?[ \t]*\z`)
	// atPrompt is the node back at a prompt of its own, exec or
	// configuration, after a decline.
	atPrompt = regexp.MustCompile(`\S[>#][ \t]*\z`)
)

// Abandon leaves question, which the node's line is waiting on, without
// answering it yes: a "[confirm]" gets "n" and a "[yes/no]:" gets "no", and
// Abandon reports true once the node is back at its prompt. Any other
// question is left as it is, typed at by nothing, and Abandon reports
// false: closing the connection to the console server does not end a
// node's command, so the line stays waiting until someone at its console
// clears it, and must not be logged out, since the logout's first Enter
// would answer it.
func Abandon(shell netcli.Shell, question string) bool {
	var decline string
	switch {
	case confirmQuestion.MatchString(question):
		decline = "n"
	case yesNoQuestion.MatchString(question):
		decline = "no"
	default:
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), logoutWait)
	defer cancel()
	if err := shell.WriteLine(ctx, decline); err != nil {
		return false
	}
	out, err := shell.ReadUntil(ctx, nodeTurn, maxBytes)
	return err == nil && atPrompt.MatchString(lastLine(out))
}

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
	if _, err := read(ctx, shell, serverPrompt, serverWait); err != nil {
		return fmt.Errorf("cmlconsole: waiting for the console server's prompt: %w", err)
	}
	if err := shell.WriteLine(ctx, "connect "+line); err != nil {
		return err
	}
	out, err := read(ctx, shell, connected, serverWait)
	if err != nil {
		return fmt.Errorf("cmlconsole: connecting to console %s: %w", line, err)
	}
	if !strings.Contains(out, "Escape character is") {
		return fmt.Errorf("cmlconsole: the console server did not connect console %s: %s", line, farside.Text(lastLine(serverPrompt.ReplaceAllString(ansi.ReplaceAllString(out, ""), ""))))
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
		out, err := read(ctx, shell, nodeTurn, nodeWait)
		if err != nil {
			return fmt.Errorf("cmlconsole: waiting for the node's login: %w", err)
		}
		last := strings.ToLower(lastLine(out))
		var answer string
		switch {
		case strings.HasPrefix(last, "username"):
			if login.Username == "" {
				return refused("the node asks for a username, and the device's credential has none")
			}
			if sentUser {
				return refused("the node refused the login (it asked for a username again)")
			}
			answer, sentUser = login.Username, true
		case strings.HasPrefix(last, "password"):
			if login.Password == "" {
				return refused("the node asks for a password, and the device's credential has none")
			}
			if sentPass {
				return refused("the node refused the login (it asked for a password again)")
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

// LogOut ends the node's exec session on the line, on a context of its
// own, so the next person on that console meets a login and not this
// session's privileged prompt. It asks for the node's prompt first and
// logs out only from an exec prompt, leaving any mode on the way: a line
// at a login prompt has no session to end, and typing there would only be
// a failed login. A line already gone, or one that does not answer, is not
// an error: there is nothing left it can log out.
func LogOut(shell netcli.Shell) error {
	ctx, cancel := context.WithTimeout(context.Background(), logoutWait)
	defer cancel()
	if err := shell.WriteLine(ctx, ""); err != nil {
		return nil
	}
	out, err := shell.ReadUntil(ctx, nodeTurn, maxBytes)
	if err != nil {
		return nil
	}
	last := strings.ToLower(lastLine(out))
	for _, login := range []string{"username", "password", "press return"} {
		if strings.HasPrefix(last, login) {
			return nil
		}
	}
	if err := shell.WriteLine(ctx, "end"); err != nil {
		return nil
	}
	if _, err := shell.ReadUntil(ctx, nodeTurn, maxBytes); err != nil {
		return nil
	}
	_ = shell.WriteLine(ctx, "exit")
	return nil
}

// read is one bounded read: up to maxBytes, until ctx ends or wait passes.
func read(ctx context.Context, shell netcli.Shell, re *regexp.Regexp, wait time.Duration) (string, error) {
	step, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	out, err := shell.ReadUntil(step, re, maxBytes)
	if err != nil && ctx.Err() == nil && step.Err() != nil {
		return out, fmt.Errorf("no answer within %s", wait)
	}
	return out, err
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
		// Cut whole characters only: at most three bytes of one go.
		l = l[:80]
		for len(l) > 0 && !utf8.ValidString(l) {
			l = l[:len(l)-1]
		}
	}
	return l
}
