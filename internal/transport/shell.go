// Shell selection: the port for a transport that can run a command more
// than one way.
package transport

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// Shell names which interpreter, if any, reads a command on the device.
//
// It is an enum rather than a boolean because "no shell" is a mode, not
// the absence of a setting, and the two Windows shells are not
// interchangeable: their metacharacter sets are disjoint (^ against the
// backtick, %VAR% against $(...), ; as an argument delimiter against ; as
// a statement separator), so a caller holding only a path string would
// have to recover which parser is on the far end by string matching.
// ShellNone is the zero value, so a task that names no shell means what
// every task meant before shells existed.
type Shell int

const (
	// ShellNone runs the command directly: Exec's own meaning, a program
	// and its arguments reaching that program exactly as written.
	ShellNone Shell = iota
	// ShellCmd runs the command through cmd.exe, the only way to reach a
	// cmd builtin such as dir, set or %ERRORLEVEL%.
	ShellCmd
	// ShellPowerShell runs the command through PowerShell.
	ShellPowerShell
)

// shellNames is the one table mapping a Shell to the token a runbook
// writes, so String and ParseShell cannot disagree.
var shellNames = map[Shell]string{
	ShellNone:       "none",
	ShellCmd:        "cmd",
	ShellPowerShell: "powershell",
}

// String returns the runbook token for s.
func (s Shell) String() string {
	if name, ok := shellNames[s]; ok {
		return name
	}
	return fmt.Sprintf("Shell(%d)", int(s))
}

// ParseShell turns a runbook's params.shell token into a Shell. An empty
// token is ShellNone. The error lists every valid value, since the useful
// reply to an unknown word is the set of known ones.
func ParseShell(token string) (Shell, error) {
	trimmed := strings.ToLower(strings.TrimSpace(token))
	if trimmed == "" {
		return ShellNone, nil
	}
	for shell, name := range shellNames {
		if name == trimmed {
			return shell, nil
		}
	}
	return ShellNone, fmt.Errorf("unknown shell %q (valid values: none, cmd, powershell)", token)
}

// ShellRequest is one command for a ShellTransport: which interpreter
// runs it, the script itself, and the values it reads.
type ShellRequest struct {
	// Shell is the interpreter.
	Shell Shell

	// Script is the command, run verbatim by Shell. Nothing is ever
	// spliced into it.
	Script string

	// Env carries a task's values to the script as environment variables
	// rather than as script text, so no value can become syntax in either
	// shell. Names are used exactly as given: naming them is the caller's
	// contract with the runbook author, not the transport's. Never a
	// secret: an environment is visible to other processes.
	Env map[string]string

	// Interpreter is the absolute path of Shell's program on the device,
	// or "" for the transport's default. It comes from the device, which
	// is the only thing that knows where its interpreters live. Ignored
	// for ShellNone, whose command names its own program.
	Interpreter string

	// WorkingDirectory is where the command starts on the device, or ""
	// to leave the choice to the far side.
	WorkingDirectory string
}

// ShellTransport is implemented by a Transport that can run a command
// through a named interpreter as well as directly.
//
// It is a sibling of Transport rather than a widened Exec, and not a
// field on Target, because Target is where to connect and nothing more,
// and Exec's promise (a command that reaches its program exactly as
// written, with no shell acting on it) must stay exactly what it is on
// every Adapter. The executor type-asserts for it only when a
// task names a shell, and refuses a task that names one its device's
// transport cannot offer.
type ShellTransport interface {
	Transport

	// ExecShell runs req against target once, authenticating with cred.
	// Its Result and error contract is Exec's exactly.
	ExecShell(ctx context.Context, target Target, cred credential.Credential, req ShellRequest) (Result, error)
}
