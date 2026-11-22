// This file is how the launch paths tell a runbook that does not compile from
// one the source could not find or read, and what each may say about it.
//
// They are told apart by the error's kind (runbook.CompileError), never by its
// text. A compile error is the author's own mistake in the author's own
// runbook, so every write that would launch it is refused with the builder's
// message: a launch, a check, a relaunch, and a schedule's write. Any other
// error may carry a storage detail, so its text stays in the log and the
// launch is left to the fan-out, as it always was (FAILURE_PATTERNS 1360).
package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
)

// refuseUncompiled returns err when it says a runbook was read and does not
// compile, and nil for every other error and for none: only that kind is a
// refusal at the write.
func refuseUncompiled(err error) error {
	var notCompiled *runbook.CompileError
	if errors.As(err, &notCompiled) {
		return notCompiled
	}
	return nil
}

// compileMessage is the sentence a caller may read when err says a runbook
// does not compile (runbook.CompileError.Message: the builder's own text, on
// one line, escaped and bounded), and empty for every other error.
func compileMessage(err error) string {
	var notCompiled *runbook.CompileError
	if errors.As(err, &notCompiled) {
		return notCompiled.Message()
	}
	return ""
}

// CompileMessage is compileMessage for the server-rendered views, which launch
// through the same Dispatcher and attach the same sentence to their form.
func CompileMessage(err error) string { return compileMessage(err) }

// RunbookCatalog is the runbook kind's launch.KindCatalog over the source a
// dispatch resolves through: List is the source's, and Verify compiles the
// definition, so a template cannot be created over a runbook that is missing
// or does not compile.
//
// A missing runbook is launch.ErrDefinitionNotFound, as before. One that does
// not compile is launch.ErrInvalidTemplate carrying the builder's message,
// since the submission names a definition nothing can launch and its author
// can act on the message. Any other error is a source failure, returned as it
// is for the caller to log and not show.
func RunbookCatalog(runbooks runbook.Source) launch.KindCatalog {
	return launch.KindCatalogFuncs{
		ListFunc: runbooks.List,
		VerifyFunc: func(ctx context.Context, definition string) error {
			_, err := runbooks.Get(ctx, definition)
			switch {
			case err == nil:
				return nil
			case errors.Is(err, runbook.ErrNotFound):
				return fmt.Errorf("%w: no runbook %q", launch.ErrDefinitionNotFound, definition)
			case compileMessage(err) != "":
				return fmt.Errorf("%w: %s", launch.ErrInvalidTemplate, compileMessage(err))
			default:
				return fmt.Errorf("resolving runbook %q: %w", definition, err)
			}
		},
	}
}
