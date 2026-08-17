package transport

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// Shell names which command interpreter, if any, runs a script on the far
// side.
//
// This is an enum rather than a boolean because "no shell" is a third
// mode and not the absence of a setting. A Windows target can run a
// command three genuinely different ways: as a program with an argument
// vector that nothing parses, through cmd.exe so its builtins (dir, set,
// for, %ERRORLEVEL%) are reachable at all, or through PowerShell so
// cmdlets and the pipeline are. Those are three destinations, and no
// setting of one boolean selects among three.
//
// The two Windows shells are not interchangeable and cannot be collapsed
// into one "shell path" string the way a POSIX device's /bin/sh can be.
// Their metacharacter sets are disjoint (^ against `, %VAR% against
// $(...), ; as an argument delimiter against ; as a statement separator),
// so they need different escaping, and a caller holding only a path
// string would have to recover which parser is on the far end by
// string-matching "powershell.exe" inside it. Naming the mode makes the
// answer data.
//
// ShellNone is the zero value on purpose: every existing binding, task
// and test that never mentions a shell keeps exactly today's meaning
// with no edit.
type Shell int

const (
	// ShellNone runs the program directly, with an argument vector no
	// interpreter parses. This is what transport.Transport.Exec has
	// always meant on SSH.
	ShellNone Shell = iota

	// ShellCmd runs the script through cmd.exe, which is the only way to
	// reach a cmd builtin: dir, set, for and %ERRORLEVEL% are not
	// programs on disk.
	ShellCmd

	// ShellPowerShell runs the script through PowerShell, for cmdlets,
	// the pipeline and the language.
	ShellPowerShell
)

// shellNames is the one table mapping a Shell to the token a runbook
// writes, so String and ParseShell cannot disagree about the vocabulary
// and adding a mode is one line rather than two that can drift.
var shellNames = map[Shell]string{
	ShellNone:       "none",
	ShellCmd:        "cmd",
	ShellPowerShell: "powershell",
}

// String returns the runbook token for s, so a Shell can be logged and
// reported in the same words an author wrote.
func (s Shell) String() string {
	if name, ok := shellNames[s]; ok {
		return name
	}
	return fmt.Sprintf("Shell(%d)", int(s))
}

// ParseShell turns a runbook's params.shell token into a Shell. An empty
// string is ShellNone, so omitting the parameter and writing "none" mean
// the same thing rather than one of them being an error.
//
// The error lists every valid value, because the author reading it chose
// a word this platform does not know and the useful reply is the set it
// does know, not a complaint.
func ParseShell(name string) (Shell, error) {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	if trimmed == "" {
		return ShellNone, nil
	}
	for shell, token := range shellNames {
		if token == trimmed {
			return shell, nil
		}
	}
	// Built in a fixed order rather than by ranging the map, so the
	// message is identical every time an author sees it.
	valid := []string{
		shellNames[ShellNone],
		shellNames[ShellCmd],
		shellNames[ShellPowerShell],
	}
	return ShellNone, fmt.Errorf("unknown shell %q (valid values: %s)", name, strings.Join(valid, ", "))
}

// ShellTransport is the sibling port for a transport that can run a
// script through a named interpreter on the far side.
//
// It is a second interface rather than a widened Exec, and rather than a
// new field on Target, for two reasons that are both recorded in the
// types themselves. Target's own doc comment says it is "a host and a
// port, nothing more", and a shell selector is neither. And widening
// Exec would force every transport to answer a question most of them
// cannot: SSH runs a command verbatim and has no business claiming a
// Windows shell mode. A protocol shape that fits neither existing port
// gets a third interface.
//
// internal/transport/winrm implements both this and Transport;
// internal/transport/ssh implements only Transport. A caller wanting a
// shell type-asserts for this interface and reports a clear refusal when
// the assertion fails, which is how a task asking for a shell the bound
// transport cannot offer becomes an explained error rather than a silent
// fallback to a different execution mode than the author asked for.
type ShellTransport interface {
	// ExecShell runs script on target through shell, authenticating with
	// cred.
	//
	// The Result and error contract is Transport.Exec's exactly: a
	// non-zero Result.ExitCode is the remote script reporting failure and
	// is NOT a Go error, while a non-nil error means the outcome could
	// not be determined at all.
	ExecShell(ctx context.Context, target Target, cred credential.Credential, shell Shell, script string) (Result, error)
}
