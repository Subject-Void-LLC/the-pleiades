// The record a testgate run leaves for a reader that is not a person
// (flakegate.Run): settled from how the run ended and written whatever the
// outcome, so a run that failed or could not finish says so in it rather
// than leaving nothing to read.
package main

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// finishRecord settles rec's verdict from runErr, how the run ended, and
// writes it to path; an empty path writes nothing. It returns runErr, or,
// when the run passed and the record could not be written, that failure:
// a caller that asked for a record and got none must not read a pass.
func finishRecord(path string, rec *flakegate.Run, runErr error) error {
	if path == "" {
		return runErr
	}
	rec.Seconds = math.Round(time.Since(rec.Started).Seconds()*10) / 10
	switch {
	case runErr == nil:
		rec.Verdict = flakegate.VerdictPass
	case countsAgainst(rec.Failures):
		rec.Verdict, rec.Error = flakegate.VerdictFail, scrub(runErr.Error())
	default:
		rec.Verdict, rec.Error = flakegate.VerdictError, scrub(runErr.Error())
	}
	if err := flakegate.WriteRun(path, *rec); err != nil {
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "testgate: %v\n", err)
			return runErr
		}
		return err
	}
	return runErr
}

// countsAgainst reports whether any of fs counts against its run: one that
// failed, or one that was never re-run.
func countsAgainst(fs []flakegate.RunFailure) bool {
	for _, f := range fs {
		if f.Outcome == flakegate.OutcomeFailed || f.Outcome == flakegate.OutcomeNotRerun {
			return true
		}
	}
	return false
}
