// Whether a check may stage a change in a device's own staging area, which
// only the command line allows.
package sdk

// StagedChecks is implemented by a RunbookContext that says whether a check
// may stage a change in a device's own staging area and discard it, as a
// check of net.netconf.config through the candidate datastore does. Only the
// command line (pleiades run and adhoc) answers true: a check job on the Walk
// tier promises to send no write at all, and nothing there yet holds a check
// to its staging area's RPCs.
type StagedChecks interface {
	// StagedChecksAllowed reports whether a check may stage a change.
	StagedChecksAllowed() bool
}

// StagedChecksAllowed reports whether rc allows a check to stage a change:
// false for a context that does not say.
func StagedChecksAllowed(rc RunbookContext) bool {
	s, ok := rc.(StagedChecks)
	return ok && s.StagedChecksAllowed()
}
