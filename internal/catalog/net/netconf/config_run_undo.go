// Package netconf: one run's undo: planned from the read before the edit,
// recorded before the edit when that read decides it, otherwise after the
// read after, and taken back when the edit changed nothing.
package netconf

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/secretref"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

// undoRun is one run's undo: planned from the read before, recorded before
// the edit when that read decides it, and otherwise after the read after.
type undoRun struct {
	rc       sdk.RunbookContext
	req      configRequest
	plan     *plan
	before   []*yangdoc.Element
	explicit bool
	// recorded says the undo was recorded before the edit.
	recorded bool
	// why is why no undo can be recorded, "" while one can.
	why string
}

// newUndo plans the undo of req against before and records it at once when
// the read before decides it. Content naming a secret plans none, so no
// secret can reach the run's journal.
func newUndo(rc sdk.RunbookContext, session netconfSession, req configRequest, before []*yangdoc.Element) (*undoRun, error) {
	u := &undoRun{rc: rc, req: req, before: before, explicit: session.WithDefaults() == netconf.WithDefaultsExplicit}
	if secretref.Has(req.content) {
		u.why = "content names a secret, and an undo is never recorded for such a call, so no secret can reach the run's journal"
		return u, nil
	}
	content, err := undoParse(req.content)
	if err != nil {
		return nil, err
	}
	u.plan = planUndo(content, before, req.defaultOperation)
	switch {
	case u.plan.why != "":
		u.why = u.plan.why
	case len(u.plan.deferred) == 0:
		return u, u.record()
	}
	return u, nil
}

// record records the plan's undo, or keeps why it cannot be.
func (u *undoRun) record() error {
	doc, why := undoContent(u.plan, credentialValues(u.rc.InjectSecrets()), u.explicit)
	switch {
	case why != "":
		u.why = why
	case doc != "":
		if err := recordUndo(u.rc, u.req, doc); err != nil {
			return err
		}
		u.recorded = true
	}
	return nil
}

// lost is what the task says when the edit or the read after failed: an undo
// left to the read after was never recorded.
func (u *undoRun) lost() []string {
	if u.recorded || u.why != "" || u.plan == nil || len(u.plan.deferred) == 0 {
		return nil
	}
	return []string{"no undo is recorded: the edit's undo could be decided only by reading the configuration after it, which did not happen"}
}

// finish completes the undo once the read after has answered: an undo
// recorded before an edit that changed nothing is taken back, one left to the
// read after is decided and recorded, and a change with no undo says why.
func (u *undoRun) finish(changed bool, after []*yangdoc.Element) ([]string, error) {
	if !changed {
		if u.recorded {
			return nil, withdrawUndo(u.rc)
		}
		return nil, nil
	}
	if !u.recorded && u.why == "" {
		u.plan.resolve(u.before, after)
		if u.plan.why != "" {
			u.why = u.plan.why
		} else if err := u.record(); err != nil {
			return nil, err
		}
	}
	switch {
	case u.recorded:
		return nil, nil
	case u.why == "":
		u.why = "the content matched what the device held, yet its configuration changed (a value the device stores in another form), so no step of an undo could be named"
	}
	return []string{"the edit changed the configuration and no undo is recorded: " + u.why}, nil
}
