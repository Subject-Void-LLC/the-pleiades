// What a pr-check receipt asks of the run's report: that it is a pass, of
// the very commit the receipt names, with nothing missing and no problem
// listed. Since make pr-check LEGS=... can keep records from an earlier run,
// the gate's own exit status no longer says every part ran at this commit;
// the report, whose merge names a record of another commit stale and one
// of a dirty tree dirty, does.
package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/prreport"
)

// checkReport reads the pr-check report at path and refuses it unless it
// is a pass of commit with nothing missing and no problem listed. It
// returns when the report's oldest record began (nil when no record says),
// which the receipt keeps so its age is counted from there.
func checkReport(path, commit string) (*time.Time, error) {
	r, err := prreport.ReadReport(path)
	if err != nil {
		return nil, fmt.Errorf("gatereceipt write: the pr-check report could not be read (%v), so nothing says every part of the run passed at this commit and no receipt was written", err)
	}
	if r.Commit != commit {
		return nil, fmt.Errorf("gatereceipt write: the pr-check report at %s is for %s and HEAD is %s, so it says nothing about this commit and no receipt was written.\n"+
			"Run `make pr-check` again", path, describeCommit(r.Commit), short(commit))
	}
	if r.Verdict != flakegate.VerdictPass {
		return nil, fmt.Errorf("gatereceipt write: the pr-check report at %s says %s, not pass, so no receipt was written.%s\n"+
			"`go run ./tools/prreport` shows why; run the parts it names again (make pr-check LEGS=...) or the whole check", path, r.Verdict, listProblems(r))
	}
	if len(r.Missing) > 0 || len(r.Problems) > 0 {
		// A pass with something missing or a problem is a report this
		// command does not trust, whoever wrote it: the merge never writes
		// one, so it was edited or written by a tool that disagrees.
		return nil, fmt.Errorf("gatereceipt write: the pr-check report at %s says pass and still lists %d missing part(s) and %d problem(s), so it cannot be read as a pass and no receipt was written.%s",
			path, len(r.Missing), len(r.Problems), listProblems(r))
	}
	return r.Oldest, nil
}

// listProblems is each part a report is missing and each problem it lists,
// one to a line, or nothing.
func listProblems(r prreport.Report) string {
	var b strings.Builder
	for _, name := range r.Missing {
		fmt.Fprintf(&b, "\n  missing: %s", name)
	}
	for _, p := range r.Problems {
		if p.Code == prreport.ProblemMissing {
			continue // named above
		}
		where := ""
		if p.Record != "" {
			where = " " + p.Record + ":"
		}
		fmt.Fprintf(&b, "\n  %s:%s %s", p.Code, where, p.Detail)
	}
	return b.String()
}

// describeCommit names a report's commit in a refusal, saying so when it
// names none.
func describeCommit(commit string) string {
	if commit == "" {
		return "no commit"
	}
	return short(commit)
}
