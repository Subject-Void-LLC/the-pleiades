// Package journal: who holds the run counter's lock, so a tick that waits
// too long can say.
//
// A tick holds the counter's lock (clock.go) for one small read and one
// synced write, so a wait that runs out means its holder is stopped (a
// terminal's Ctrl-Z), or stuck inside that write (a disk or a machine out of
// memory stalling the sync). The refusal used to say only that the lock was
// held, which left nothing to act on. The holder now writes, beside the lock
// and only while it holds it, its process id, its command and when it took
// the lock, and a tick that gives up names them. Nothing from the command
// line beyond the command's name is written, since an argument can hold a
// secret (an adhoc parameter).
package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// clockHolderName is the file beside the lock naming its holder.
const clockHolderName = "clock.holder"

// maxClockHolderBytes bounds the holder file.
const maxClockHolderBytes = 512

// clockHolder is what the holder file says.
type clockHolder struct {
	// PID is the holding process's id.
	PID int `json:"pid"`
	// Command is the program and its command word ("pleiades adhoc"), never
	// an argument.
	Command string `json:"command"`
	// Since is when it took the lock.
	Since time.Time `json:"since"`
}

// commandWord is the shape of a command word pleiades takes: lowercase
// letters and hyphens, so a flag's value or a parameter is never taken for
// one.
var commandWord = regexp.MustCompile(`^[a-z][a-z-]{0,40}$`)

// commandOf names this process for the holder file: its program's base name
// and the first argument that reads as a command word.
func commandOf(args []string) string {
	if len(args) == 0 {
		return ""
	}
	name := filepath.Base(args[0])
	if !commandWord.MatchString(strings.TrimSuffix(name, ".exe")) {
		name = "a process"
	}
	for _, a := range args[1:] {
		if commandWord.MatchString(a) {
			return name + " " + a
		}
	}
	return name
}

// writeClockHolder records this process as the lock's holder in dir, taken
// at now, and returns the function that clears it again. Writing it is best
// effort: a holder file that cannot be written only leaves a waiter unable to
// name the holder.
func writeClockHolder(dir string, now time.Time) func() {
	path := filepath.Join(dir, clockHolderName)
	raw, err := json.Marshal(clockHolder{PID: os.Getpid(), Command: commandOf(os.Args), Since: now.UTC()})
	if err != nil {
		return func() {}
	}
	// #nosec G304 -- the project's own .pleiades directory joined with this
	// package's fixed name; noFollow refuses a link put at the name.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|noFollow, fileMode)
	if err != nil {
		return func() {}
	}
	_, writeErr := f.Write(raw)
	if closeErr := f.Close(); writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return func() {}
	}
	return func() { _ = os.Remove(path) }
}

// describeClockHolder says, for a tick that gave up at now, who holds the
// lock in dir: its process id, command and how long it has held it, or that
// the holder did not say (a build from before the holder file, or one
// between taking the lock and writing it).
func describeClockHolder(dir string, now time.Time) string {
	raw, ok := readPrivate(filepath.Join(dir, clockHolderName), maxClockHolderBytes)
	var h clockHolder
	if !ok || json.Unmarshal(raw, &h) != nil || h.PID <= 0 {
		return "by a process that did not say which: a tick holds it for one small write, so its holder is stopped or stuck; nothing was run"
	}
	held := now.Sub(h.Since).Round(time.Millisecond)
	// The file is this package's, but any process of the operator's can write
	// it, so its text is escaped before it reaches a terminal.
	return fmt.Sprintf("by process %d (%s), which took it at %s and has held it for %s: a tick holds it for one small write, so that process is stopped or stuck (resume it, or end it); nothing was run",
		h.PID, termsafe.EscapeLine(h.Command), h.Since.Format(time.RFC3339), held)
}
