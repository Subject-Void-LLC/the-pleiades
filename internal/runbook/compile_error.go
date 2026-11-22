// Package runbook: CompileError, the kind of error a Source returns for a
// runbook it found and read and could not build, which a caller tells apart
// from a lookup or storage failure by its type, never by its text.
package runbook

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// MaxCompileMessage bounds Message, in bytes: the whole sentence, with the
// builder's text escaped and the note saying it was cut. A build error names
// a node, a task, a key or an expression, in a sentence or two; the bound is
// for a runbook written to make it long.
const MaxCompileMessage = 2048

// CompileError says the runbook ID was found and read, and does not build.
//
// It is a kind of its own because the two things that can go wrong resolving
// a runbook belong to different readers. A storage failure can name a
// filesystem path, so its text stays in the server's log. A build failure is
// the author's own mistake in the author's own runbook: the source builds
// from the file's bytes alone (engine.Builder.BuildFromYAML, which is given
// no directory, so it opens nothing and names no path), and its text names
// what the author wrote and nothing of the server's. Withholding it leaves
// the author with nothing to fix (FAILURE_PATTERNS 1360).
//
// A caller reaches it with errors.As through any wrapping.
type CompileError struct {
	// ID is the runbook's id, already held to ValidID.
	ID string

	// Err is the builder's own error.
	Err error
}

// Error implements error, in the words the source has always used.
func (e *CompileError) Error() string {
	return fmt.Sprintf("failed to compile runbook %q: %v", e.ID, e.Err)
}

// Unwrap exposes the builder's error, so errors.Is reaches a build error a
// caller tests for (engine.ErrTaskList).
func (e *CompileError) Unwrap() error { return e.Err }

// Message is the sentence a caller outside the server may read: an API
// answer, a form's refusal, a job's failure reason.
//
// The builder's text quotes what the runbook's author wrote (a handler's
// name, a key, an expression), so it is author input wherever it is shown,
// and three things are done to it, in this order:
//
//   - It is masked by shape (redact.Text with no known values, the masker
//     logs and test output go through), so a value that looks like a
//     credential, written literally into the runbook, is not repeated to
//     whoever may launch it or read its job. Shape is all there is to go by:
//     a secret that looks like an ordinary word is shown.
//   - It is kept to one line, with every character a terminal acts on written
//     as a visible escape (termsafe.EscapeLine).
//   - It is cut so the whole sentence is at most MaxCompileMessage bytes, with
//     the text's own length after it. The cut is made on the escaped text and
//     between characters, so it never ends inside an escape or a multi-byte
//     character.
func (e *CompileError) Message() string {
	text := redact.Text(nil, e.Err.Error())
	prefix := fmt.Sprintf("runbook %q does not compile: ", e.ID)
	escaped := termsafe.EscapeLine(text)
	if len(prefix)+len(escaped) <= MaxCompileMessage {
		return prefix + escaped
	}

	note := fmt.Sprintf("... (cut from %d bytes)", len(text))
	room := MaxCompileMessage - len(prefix) - len(note)
	var kept strings.Builder
	for i := 0; i < len(text); {
		// One character at a time, so each escape is whole or absent. An
		// invalid byte decodes as one byte and is escaped as one.
		_, size := utf8.DecodeRuneInString(text[i:])
		unit := termsafe.EscapeLine(text[i : i+size])
		if kept.Len()+len(unit) > room {
			break
		}
		kept.WriteString(unit)
		i += size
	}
	return prefix + kept.String() + note
}
