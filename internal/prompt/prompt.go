// Package prompt reads secrets from a terminal without echoing them.
//
// It exists because two binaries need the identical behavior and a copy in
// each would be two places to get it subtly wrong. `pleiades add-credential`
// reads an SSH password or a key passphrase; `controller bootstrap-admin`
// and `reset-password` read a local account password. Both must keep the
// value off the screen, out of the scrollback buffer, and out of the
// process argument list.
//
// The argument list is the part worth stating plainly, because a flag looks
// like the convenient option: a secret passed as `--password hunter2` is
// visible in shell history and in /proc to every other user on the machine
// for as long as the process runs. Prompting is the default for that reason
// rather than for tidiness, and the non-interactive escape hatches are
// per-command decisions those commands document themselves.
package prompt

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// Secret prints prompt to stderr and reads one line from stdin with
// terminal echo disabled.
//
// stderr rather than stdout, so a command whose stdout carries machine
// readable output does not interleave a human prompt into it.
//
// It requires stdin to be a real terminal. Callers that must supply a
// secret non-interactively should offer their own explicit flag or file
// path rather than making this function fall back to a plain read: a
// silent fallback would mean a piped secret is echoed on some machines and
// not others, which is the kind of difference nobody notices until it is in
// a CI log.
func Secret(promptText string) (string, error) {
	return SecretFrom(os.Stderr, int(os.Stdin.Fd()), promptText)
}

// SecretFromStdin reads one secret as a line on standard input.
//
// The explicit non-interactive path, for a container start-up script, a
// provisioning tool or an automated test, which have no terminal and must
// not be forced to invent one. It is opted into by a flag rather than
// entered automatically when stdin is not a terminal: an automatic fallback
// means the same command echoes a secret on some machines and not others,
// which is the kind of difference nobody notices until it is in a CI log.
//
// This is still not a flag VALUE. The secret arrives on a pipe, so it never
// appears in shell history and never appears in this process's argument
// list, which is the property the interactive prompt exists to preserve and
// the one a --password flag would give away.
func SecretFromStdin() (string, error) {
	return secretFromReader(os.Stdin)
}

func secretFromReader(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	// A generous cap so a long passphrase is not silently truncated at
	// bufio's 64 KiB default, and a cap at all so a pipe that never ends
	// does not read forever.
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)

	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("failed to read a secret from standard input: %w", err)
		}
		return "", fmt.Errorf("no secret on standard input")
	}
	// Trailing carriage return stripped, so a value piped from a file
	// written on Windows is the value the operator meant rather than one
	// with an invisible byte on the end that makes every later login fail.
	secret := strings.TrimSuffix(scanner.Text(), "\r")
	if secret == "" {
		return "", fmt.Errorf("an empty secret is not allowed")
	}
	return secret, nil
}

// SecretFrom is Secret with the output stream and file descriptor injected,
// so a test can exercise the non-terminal refusal without a pseudo
// terminal.
func SecretFrom(out io.Writer, fd int, promptText string) (string, error) {
	fmt.Fprint(out, promptText)

	secret, err := term.ReadPassword(fd)
	fmt.Fprintln(out)
	if err != nil {
		return "", fmt.Errorf("failed to read a secret from the terminal: %w", err)
	}
	if len(secret) == 0 {
		// Refused rather than returned, because an empty secret is almost
		// always a mistyped prompt rather than an intention, and the
		// alternative is storing a credential nobody can use.
		return "", fmt.Errorf("an empty secret is not allowed")
	}
	return string(secret), nil
}
