// Package netconf: net.netconf.config's undo written and recorded: the undo
// document assembled on one line, a deferred group decided from the reads
// before and after, and the gates an undo passes before the run's journal may
// keep it (reads in explicit with-defaults mode, its bounds, no secret,
// nothing masking would hide).
package netconf

import (
	"fmt"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/secretref"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// maxUndoBytes is the most text the run's journal keeps of one undo value
// (internal/engine's maxRecordedText, which a Collection cannot import, and
// internal/archtest holds this copy equal to): past it, the journal keeps
// none of the undo and a rollback refuses the task as not recorded in full,
// so a longer undo is not recorded at all, and the run says so.
const maxUndoBytes = 256

// place adds ops to the undo document top under path, the containers above
// them, each container written once.
func place(top []*yangdoc.Element, path []*yangdoc.Element, ops ...*yangdoc.Element) []*yangdoc.Element {
	if len(path) == 0 {
		return append(top, ops...)
	}
	for _, have := range top {
		if have.Name == path[0].Name && len(have.Attrs) == 0 {
			have.Children = place(have.Children, path[1:], ops...)
			return top
		}
	}
	step := path[0].Copy(nil)
	step.Children = place(nil, path[1:], ops...)
	return append(top, step)
}

// resolve decides p's deferred groups from before and after, the reads around
// the edit, adding each one's undo to p, or setting p.why for one the reads
// cannot attribute.
func (p *plan) resolve(before, after []*yangdoc.Element) {
	for _, g := range p.deferred {
		ops, why := decideGroup(instancesAt(before, g), instancesAt(after, g), g.content)
		if why != "" {
			p.why = fmt.Sprintf("the reads cannot attribute what the edit did to %s: %s", g.name.Local, why)
			return
		}
		if len(ops) > 0 {
			p.top = place(p.top, g.path, ops...)
		}
	}
	p.deferred = nil
}

// instancesAt is every instance of g's name under g's containers in read.
func instancesAt(read []*yangdoc.Element, g group) []*yangdoc.Element {
	level := read
	for _, step := range g.path {
		found := yangdoc.FindIn(level, step.Name)
		if len(found) != 1 {
			return nil
		}
		level = found[0].Children
	}
	return yangdoc.FindIn(level, g.name)
}

// decideGroup is the undo of what an edit did to one group, from its
// instances before and after, each compared whole, and the content's
// elements there: nothing when the group is as it was; when nothing that was
// there changed, the removal of each new instance, each attributed to a
// content element of its own whose every leaf it holds, so an instance
// another client added between the reads is never taken for this task's.
// Anything else is why there is no undo.
func decideGroup(before, after, content []*yangdoc.Element) ([]*yangdoc.Element, string) {
	gone := missingFrom(before, after)
	added := missingFrom(after, before)
	switch {
	case len(gone) == 0 && len(added) == 0:
		return nil, ""
	case len(gone) > 0:
		return nil, whyPutBack
	case len(added) > len(content):
		return nil, fmt.Sprintf("%d instances are new and the content names %d, so another client may have added one between the reads", len(added), len(content))
	}
	used := make([]bool, len(content))
	out := make([]*yangdoc.Element, 0, len(added))
	for _, y := range added {
		e := attribute(y, content, used)
		if e == nil {
			return nil, "a new instance matches no element of the content, so another client may have added it between the reads"
		}
		out = append(out, removal(e))
	}
	return out, ""
}

// attribute is the first content element not yet used whose every leaf y
// holds with the same value, marked used; nil when there is none.
func attribute(y *yangdoc.Element, content []*yangdoc.Element, used []bool) *yangdoc.Element {
	for i, e := range content {
		if used[i] || e.IsLeaf() {
			continue
		}
		holds := true
		for _, l := range allLeaves(e) {
			if !holdsValue(y, l) {
				holds = false
				break
			}
		}
		if holds {
			used[i] = true
			return e
		}
	}
	return nil
}

// holdsValue reports whether y has a child of l's name with l's value.
func holdsValue(y, l *yangdoc.Element) bool {
	for _, c := range y.Find(l.Name) {
		if c.IsLeaf() && yangdoc.CanonicalValue(c) == yangdoc.CanonicalValue(l) {
			return true
		}
	}
	return false
}

// missingFrom are the elements of a with no element of the same canonical
// form in b.
func missingFrom(a, b []*yangdoc.Element) []*yangdoc.Element {
	have := map[string]int{}
	for _, y := range b {
		have[yangdoc.CanonicalElements([]*yangdoc.Element{y})]++
	}
	var out []*yangdoc.Element
	for _, x := range a {
		k := yangdoc.CanonicalElements([]*yangdoc.Element{x})
		if have[k] > 0 {
			have[k]--
			continue
		}
		out = append(out, x)
	}
	return out
}

// undoContent is p's undo as the content of a net.netconf.config call, or why
// it is not one the run's journal may keep: reads not in with-defaults
// explicit mode (explicit false), where a leaf set to its default can read as
// absent and its removal would unset it; an attribute it cannot write; longer
// than maxUndoBytes; holding text a terminal acts on, a template opening the
// method would read as a secret reference, a masked placeholder, or anything
// masking would hide (a secret-named leaf, a crypt-shaped value, the run's own
// credential). credentials are the credential's values.
func undoContent(p *plan, credentials []string, explicit bool) (string, string) {
	if len(p.top) == 0 {
		return "", ""
	}
	if !explicit {
		return "", "the device does not read in with-defaults explicit mode, so a read cannot tell a value set to its default from one not set, and no undo would be exact"
	}
	doc, err := yangdoc.Write(p.top)
	if err != nil {
		return "", "its undo cannot be written: " + err.Error()
	}
	switch {
	case len(doc) > maxUndoBytes:
		return "", fmt.Sprintf("its undo is %d bytes, and the run's journal keeps one of at most %d", len(doc), maxUndoBytes)
	case !utf8.ValidString(doc) || !yanglib.Printable(doc):
		return "", "its undo holds text the run's journal does not keep (a control or formatting character)"
	case collection.IsTemplate(doc) || secretref.Has(doc):
		return "", "its undo holds {{, {% or {#, which net.netconf.config would read as a secret reference"
	case netcfg.RefuseMasked(paramContent, doc) != nil:
		return "", "its undo holds a masked placeholder"
	}
	masked, found, err := netcfg.MaskXML(doc, credentials...)
	if err != nil || len(found) > 0 || masked != doc {
		return "", "the content it would remove holds what masking hides (a secret), which the run's journal never keeps"
	}
	return doc, ""
}

// recordUndo records the undo of a run of req, whose content is undo: this
// method again with undo as its content, default_operation none, and the
// datastore the task named, if it named one. It stands even if the task
// fails, since running it where nothing changed changes nothing: it removes
// only what the read before found absent.
func recordUndo(rc sdk.RunbookContext, req configRequest, undo string) error {
	params := map[string]any{paramContent: undo, paramDefaultOperation: netconf.DefaultOperationNone}
	if req.target != "" {
		params[paramDatastore] = string(req.target)
	}
	return sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:          configFQCN,
		Params:        params,
		Description:   "removes what this run created: " + undo,
		KeepOnFailure: true,
	})
}

// withdrawUndo takes back an undo recorded before an edit that, read after,
// changed nothing, so the run's report shows no undo for a task that made no
// change. A rollback would skip it anyway, since it undoes only changes.
func withdrawUndo(rc sdk.RunbookContext) error {
	return rc.SetStat(sdk.StatInverse, nil)
}

// configUndoCall is net.netconf.config's answer, from a call alone, to
// whether it records the undo its manifest declares: content naming a secret
// records none, so no secret can reach the run's journal, and neither does
// default_operation replace, which replaces the whole datastore. Every other
// call's undo depends on what the device holds, which only the run reads.
func configUndoCall(params map[string]any) string {
	if content, ok := params[paramContent].(string); ok && secretref.Has(content) {
		return "content names a secret, and a call whose content names one records no undo, so no secret can reach the run's journal"
	}
	if op, ok := params[paramDefaultOperation].(string); ok && op == netconf.DefaultOperationReplace {
		return "default_operation replace replaces the whole datastore, whose earlier contents the run's journal cannot keep, so it records no undo"
	}
	return ""
}

// undoParse parses content for planning, which readRequest has already held
// to be well-formed.
func undoParse(content string) ([]*yangdoc.Element, error) {
	els, err := yangdoc.Parse([]byte(content))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", paramContent, err)
	}
	return els, nil
}
