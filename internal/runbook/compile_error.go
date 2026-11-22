// Package runbook: CompileError, the kind of error a Source returns for a
// runbook it found and read and could not build, which a caller tells apart
// from a lookup or storage failure by its type, never by its text.
package runbook

import (
	"fmt"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// MaxCompileMessage bounds how many bytes of the builder's own text
// CompileError.Message shows. A build error names a node, a task, a key or
// an expression, in a sentence or two; the bound is for a runbook written to
// make it long.
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
// name, a key, an expression), so it is author input wherever it is shown:
// it is kept to one line with every character a terminal acts on written as
// a visible escape (termsafe.EscapeLine), and cut at MaxCompileMessage bytes
// with its whole length after it.
func (e *CompileError) Message() string {
	text := e.Err.Error()
	cut := ""
	if len(text) > MaxCompileMessage {
		end := MaxCompileMessage
		// Back up to the start of a character, so the cut does not show as
		// an invalid byte the text never held there.
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		cut = fmt.Sprintf("... (cut from %d bytes)", len(text))
		text = text[:end]
	}
	return fmt.Sprintf("runbook %q does not compile: %s%s", e.ID, termsafe.EscapeLine(text), cut)
}
