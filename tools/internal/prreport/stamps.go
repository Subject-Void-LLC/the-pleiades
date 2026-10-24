// Tying each record to the run that checked one commit: a record stamped
// with another commit, or none, is stale, and a testgate run or step that
// began on a tree with uncommitted changes is dirty. Either keeps a report
// from passing, which is what lets make pr-check LEGS=... keep the records
// of the parts it does not run: a kept record counts only when it is for
// the same commit, on a clean tree. The report's oldest record is found
// here too, for the receipt's age bound.
package prreport

import (
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

// checkStamp records a problem for file, a testgate run's or a step's
// record stamped with commit and clean: stale when commit is not the
// report's (or absent), dirty when it is stamped and its tree was not
// clean. A record nobody stamped is stale and not also dirty, since
// nothing says what its tree held. With no commit in the report, no
// record can match it; unstampedReport says that once instead.
func (r *Report) checkStamp(file, commit string, clean bool) {
	if commit == "" {
		if r.Commit != "" {
			r.problem(ProblemStale, file, "the record names no commit, so it cannot be tied to "+short(r.Commit)+", the commit pr-check checked: it may be an earlier run's")
		}
		return
	}
	if r.Commit != "" && commit != r.Commit {
		r.problem(ProblemStale, file, "the record is for "+short(commit)+" and pr-check checked "+short(r.Commit)+": it is an earlier run's, so run that part again")
	}
	if !clean {
		r.problem(ProblemDirty, file, "the run began on a working tree with uncommitted changes, so it tested code that is in no commit")
	}
}

// checkLeg ties an OS check's leg to the report's commit, and counts its
// start toward the oldest record. A leg tests the tree git archive makes of
// its commit, never the working tree, so it is clean by construction and
// carries no clean field: only its commit is compared. A check with no
// machine set up tested nothing, keeps its treatment (said, and not
// failed), and is neither stale nor counted.
func (r *Report) checkLeg(file string, leg oscheck.LegRecord) {
	if NotConfigured(leg) {
		return
	}
	r.checkStamp(file, leg.Commit, true)
	r.began(leg.Started)
}

// unstampedReport records one stale problem for a report that names no
// commit and holds records a commit could have been checked against.
func (r *Report) unstampedReport() {
	if r.Commit != "" {
		return
	}
	records := len(r.Passes) + len(r.Steps)
	for _, l := range r.Legs {
		if !NotConfigured(l.Leg) {
			records++
		}
	}
	if records > 0 {
		r.problem(ProblemStale, "", "the report names no commit, so none of its records can be tied to the commit pr-check checked")
	}
}

// began moves r.Oldest back to at when at is earlier; the zero time, a
// record that does not say, moves nothing.
func (r *Report) began(at time.Time) {
	if at.IsZero() {
		return
	}
	at = at.UTC()
	if r.Oldest == nil || at.Before(*r.Oldest) {
		r.Oldest = &at
	}
}

// short is a commit's first twelve characters, as git log shows it.
func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
