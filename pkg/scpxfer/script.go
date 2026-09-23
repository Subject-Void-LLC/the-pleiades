// The device-side scripts each SCP transfer runs, the preflight answer they
// print, and the exit statuses they refuse with.
package scpxfer

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// Exit statuses the device-side scripts use for their own refusals.
// They sit above anything scp or a shell uses for itself, so a status
// in this range is always the script's own verdict.
const (
	exitRootMissing   = 101
	exitParentMissing = 102
	exitDeclined      = 103
	exitNoTempDir     = 104
	exitScpFailed     = 105
	exitChmodFailed   = 106
	exitRenameFailed  = 107
)

// preflight is the part of both scripts that runs before any content
// moves. It resolves the root and the parent directory physically with
// the kernel's own "cd -P" and "pwd -P", leaves the shell standing IN
// the physical parent (so every later step is relative to a directory
// that cannot be swapped out from under it), describes the final
// component without following it, prints all three NUL-terminated, and
// then waits for the client's verdict on its standard input.
//
// $(pwd -P; printf x) followed by ${p%?x} is deliberate: command
// substitution strips every trailing newline, so a directory named
// "x<newline>" next to a root named "x" would otherwise report the
// root's own path. The x sentinel keeps the newline, and the expansion
// removes only the newline pwd added and the sentinel.
const preflight = `unset CDPATH
r=$(cd -P -- ROOT 2>/dev/null && pwd -P && printf x) || exit 101
r=${r%?x}
cd -P -- PARENT 2>/dev/null || exit 102
p=$(pwd -P && printf x) || exit 102
p=${p%?x}
if [ -L BASE ]; then k=l
elif [ -d BASE ]; then k=d
elif [ -f BASE ]; then k=f
elif [ -e BASE ]; then k=o
else k=a
fi
printf '%s\0%s\0%s\0' "$r" "$p" "$k"
IFS= read -r verdict || exit 103
[ "$verdict" = y ] || exit 103
`

// putBody runs after the client approves a Put. mktemp -d creates the
// private directory 0700 in one step, so there is no moment another
// account could open the new file. The EXIT trap removes it whatever
// happens, including scp exiting because the client vanished
// mid-stream. chmod sets the requested mode exactly, and mv -f is
// rename(2), which replaces the target in one step and never follows a
// symlink planted at it.
const putBody = `t=$(mktemp -d ./.pleiades-xfer-XXXXXXXXXXXXXXXX) || exit 104
trap 'rm -rf -- "$t"' EXIT
trap 'exit 1' HUP INT TERM PIPE
scp -t -- "$t" || exit 105
chmod MODE -- "$t"/BASE || exit 106
mv -f -- "$t"/BASE ./BASE || exit 107
`

// getBody runs after the client approves a Get. The ./ prefix keeps a
// name starting with a dash from reading as an option.
const getBody = `exec scp -f -- ./BASE
`

// putScript and getScript fill the templates with QuoteArg-quoted
// values. The whole script is then quoted again as the single argument
// of /bin/sh -c, so the device's login shell (whatever it is) parses one
// quoted word and the script itself always runs under a POSIX shell.
func putScript(p filexfer.Path, mode filexfer.Mode) string {
	return wrap(fill(preflight+putBody, p, mode))
}

// getScript is putScript for a Get.
func getScript(p filexfer.Path) string {
	return wrap(fill(preflight+getBody, p, 0))
}

// fill substitutes the quoted values into a script template.
func fill(tmpl string, p filexfer.Path, mode filexfer.Mode) string {
	return strings.NewReplacer(
		"ROOT", remoteexec.QuoteArg(p.Root()),
		"PARENT", remoteexec.QuoteArg(p.Dir()),
		"BASE", remoteexec.QuoteArg(p.Base()),
		"MODE", mode.String(),
	).Replace(tmpl)
}

// wrap makes the script the one argument of /bin/sh -c.
func wrap(script string) string {
	return "/bin/sh -c " + remoteexec.QuoteArg(script)
}

// preflightAnswer is what the device reported before any content moved.
type preflightAnswer struct {
	root, parent string
	kind         filexfer.Kind
	exists       bool
}

// readPreflight reads the three NUL-terminated answers, each bounded,
// so a device cannot make this process hold an endless answer.
func readPreflight(br *bufio.Reader) (preflightAnswer, error) {
	var fields [3]string
	for i := range fields {
		var sb strings.Builder
		for {
			b, err := br.ReadByte()
			if err != nil {
				return preflightAnswer{}, noEOF(err)
			}
			if b == 0 {
				break
			}
			if sb.Len() >= filexfer.MaxPathBytes {
				return preflightAnswer{}, fmt.Errorf("preflight answer longer than %d bytes", filexfer.MaxPathBytes)
			}
			sb.WriteByte(b)
		}
		fields[i] = sb.String()
	}
	ans := preflightAnswer{root: fields[0], parent: fields[1], exists: true}
	switch fields[2] {
	case "f":
		ans.kind = filexfer.KindRegular
	case "d":
		ans.kind = filexfer.KindDirectory
	case "l":
		ans.kind = filexfer.KindSymlink
	case "o":
		ans.kind = filexfer.KindOther
	case "a":
		ans.exists = false
	default:
		return preflightAnswer{}, fmt.Errorf("unrecognized file kind %q in the preflight answer", fields[2])
	}
	return ans, nil
}

// exitError turns a script's exit status into the refusal or failure it
// stands for.
func exitError(p filexfer.Path, code int, stderr string) error {
	switch code {
	case exitRootMissing:
		return &filexfer.ContainmentError{Path: p.String(), Reason: filexfer.ContainmentRootMissing}
	case exitParentMissing:
		return &filexfer.ContainmentError{Path: p.String(), Reason: filexfer.ContainmentParentMissing}
	}
	what := map[int]string{
		exitDeclined:     "the transfer was declined",
		exitNoTempDir:    "could not create a private directory",
		exitScpFailed:    "scp failed",
		exitChmodFailed:  "could not set the mode",
		exitRenameFailed: "could not rename the file into place",
	}[code]
	if what == "" {
		what = fmt.Sprintf("the device script exited %d", code)
	}
	if stderr != "" {
		return fmt.Errorf("scpxfer: %q: %s (device stderr: %q)", p.String(), what, stderr)
	}
	return fmt.Errorf("scpxfer: %q: %s", p.String(), what)
}
