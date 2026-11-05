// The script that starts a detached command on the device, and the parsing
// of its answers (detach.go).
package remoteexec

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// detachStart starts the command it reads on standard input in the
// background, and prints the job's directory as its last line. Its arguments
// are the shell the job runs under ("$1"), the command's length in bytes
// ("$2") and the job's name ("$3", chosen by the client, so a client whose
// start call failed can still stop the job by name). It exits 70, before
// starting anything and leaving nothing behind, wherever it cannot do that
// safely, and the caller then runs the command where it stands; it exits 71
// when the command reached it incomplete, and nothing is started.
//
//   - The command goes into a 0600 file in the job's directory rather than
//     an argument, so it never shows in a process listing, where an argument
//     would for the job's whole life.
//   - The command is started only when every byte of it arrived: a channel
//     that ends partway through the send hands cat what it got and then an
//     end of input, and the prefix of a command is a different command
//     ("rm -rf /var/lib/app/cache/tmp" cut to "rm -rf /var/lib/app/"). The
//     count is read through $(( )), since BSD wc pads it.
//   - The directory is fresh and private, under $HOME/.pleiades-async-<uid>
//     (the account's id, since sudo may keep the login's HOME). Both $HOME
//     and that base must belong to the account running the job, the base
//     must not be a link, and it is made 0700: under become, root polls and
//     stops the job through these paths, so a directory the login owns would
//     let the login point root's reads and kills anywhere. The job's own
//     directory is made with mkdir, which fails on one that exists.
//   - The job runs under the caller's umask (the 077 is for the job's own
//     files) and under "$1", sh, as a plain run's command does (script.go).
//   - It runs in its own session where setsid exists, otherwise in its own
//     process group through job control, so a stop reaches everything it
//     started. Where job control cannot be had either (dash and BusyBox ash
//     turn it off with no terminal), nothing could stop the job as a whole,
//     so it is not started detached.
//   - MSYS and Cygwin (Git's sh on a Windows OpenSSH server) are refused: a
//     command written for cmd.exe would run under sh there.
//
// The exit status is written last, through a rename, so a poll never reads
// half of it.
const detachStart = `um=$(umask)
umask 077
case $(uname -s 2>/dev/null) in MINGW*|MSYS*|CYGWIN*) exit 70;; esac
[ -n "$HOME" ] && [ -O "$HOME" ] || exit 70
base="$HOME/.pleiades-async-$(id -u)"
mkdir -p "$base" || exit 70
[ -d "$base" ] && [ ! -L "$base" ] && [ -O "$base" ] || exit 70
chmod 700 "$base" || exit 70
d="$base/job.$3"
mkdir "$d" || exit 70
cat >"$d/cmd" || { rm -rf "$d"; exit 70; }
n=$(wc -c <"$d/cmd") || { rm -rf "$d"; exit 70; }
[ "$((n))" -eq "$2" ] || { rm -rf "$d"; exit 71; }
shell=${1:-${SHELL:-sh}}
job='umask "$2"; "$3" "$1/cmd" >"$1/out" 2>"$1/err" </dev/null; echo $? >"$1/rc.tmp"; mv "$1/rc.tmp" "$1/rc"'
if command -v setsid >/dev/null 2>&1; then
  setsid sh -c "$job" job "$d" "$um" "$shell" </dev/null >/dev/null 2>&1 &
else
  set -m 2>/dev/null
  case $- in *m*) ;; *) rm -rf "$d"; exit 70;; esac
  sh -c "$job" job "$d" "$um" "$shell" </dev/null >/dev/null 2>&1 &
fi
echo $! >"$d/pid"
echo "$d"`

// Exit statuses of detachStart that mean nothing was started.
const (
	// detachRunAttached: the device cannot run the command detached safely;
	// it runs where it stands.
	detachRunAttached = 70
	// detachIncomplete: the command reached the device cut short.
	detachIncomplete = 71
)

// detachAnswerLimit bounds a start's answer and each poll's: the line asked
// for, after whatever a login shell's startup files printed ahead of it.
const detachAnswerLimit = 4096

// jobDir is what a started job's directory must look like before any later
// command names it: one line, absolute, ending in the base and job names.
var jobDir = regexp.MustCompile(`^/[^\x00\n]*/\.pleiades-async-[0-9]+/job\.[A-Za-z0-9]+$`)

// newJobName returns a job's name: random, so it is never a directory a
// previous job left, and chosen here, so a start that fails after reaching
// the device can still be stopped by it.
func newJobName() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b) // crypto/rand.Read does not fail (Go 1.24 and later)
	return hex.EncodeToString(b)
}

// jobByName is the job directory for name as the device's shell spells it,
// for stopDetached when the device never answered with the directory. name
// is newJobName's hex, so it needs no quoting.
func jobByName(name string) string {
	return `"$HOME/.pleiades-async-$(id -u)/job.` + name + `"`
}

// detachStop returns the script that stops the job in qdir (a directory as
// the device's shell spells it: QuoteArg of a jobDir, or jobByName): SIGTERM
// to its process group (or the process, with no setsid), SIGKILL after a
// grace, then its directory removed. The group is named as -pid after the
// signal, not after "--", which dash's kill refuses. It exits 0 only when the
// job had a pid it could be stopped by and the stop ran to its end (a job
// that had already ended by then is stopped all the same).
//
//   - The job's base must be a directory, not a link, owned by the account,
//     inside a HOME the account owns, as detachStart requires: otherwise the
//     pid file is another account's to write, and nothing is read or removed.
//   - The pid must be what $! can be: digits with no leading zero, at most
//     seven of them (Linux's pid_max is 4194304, so no value wraps), and more
//     than 1. kill -TERM -1 signals every process the account may signal,
//     and -0 the stop's own group; a value read from a file is never trusted
//     that far. A job with no such pid (a start cut off before writing it)
//     has its directory, which holds its command, removed, and nothing is
//     signalled.
func detachStop(qdir string) string {
	return `d=` + qdir + `
b=${d%/*}
[ -d "$b" ] && [ ! -L "$b" ] && [ -O "$b" ] && [ -O "${b%/*}" ] || exit 1
p=$(cat "$d/pid" 2>/dev/null) || p=
case $p in ''|0*|*[!0-9]*) rm -rf "$d"; exit 1;; esac
[ ${#p} -le 7 ] && [ "$p" -gt 1 ] || { rm -rf "$d"; exit 1; }
kill -TERM -"$p" 2>/dev/null || kill -TERM "$p" 2>/dev/null
i=0; while [ $i -lt 3 ] && kill -0 "$p" 2>/dev/null; do sleep 1; i=$((i+1)); done
kill -KILL -"$p" 2>/dev/null || kill -KILL "$p" 2>/dev/null
rm -rf "$d"`
}

// lastLine returns the last non-empty line of text, trimmed: a start
// script's own answer, after whatever a login shell's startup files printed.
func lastLine(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\r\n\t "), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// rcPrefix marks a poll's answer, so the exit status is read from its own
// line and never from a login shell's startup output.
const rcPrefix = "pleiades-rc="

// pollRC parses a finished poll's answer: the exit status on its marked last
// line.
func pollRC(stdout string) (int, error) {
	text, ok := strings.CutPrefix(lastLine(stdout), rcPrefix)
	if !ok {
		return 0, fmt.Errorf("no exit status line in %q", boundedText(stdout))
	}
	return strconv.Atoi(strings.TrimSpace(text))
}

// afterMarker returns what follows the first line that is exactly marker in
// text: a job file's contents, after whatever a login shell's startup files
// printed ahead of the marker.
func afterMarker(text, marker string) (string, bool) {
	if rest, ok := strings.CutPrefix(text, marker+"\n"); ok {
		return rest, true
	}
	_, rest, ok := strings.Cut(text, "\n"+marker+"\n")
	return rest, ok
}

// jobShell is the shell a detached command runs under, passed to detachStart
// as "$1": sh, which runs a plain command too (script.go), unless the
// connection is Raw, whose plain commands the login shell reads.
func (c *Conn) jobShell() string {
	if c.raw {
		return ""
	}
	return "sh"
}
