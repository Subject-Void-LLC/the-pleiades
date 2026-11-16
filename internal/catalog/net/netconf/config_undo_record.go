// Package netconf: net.netconf.config's undo written and recorded: the undo
// document assembled on one line, a deferred group decided from the reads
// before and after, and the gates an undo passes before the run's journal may
// keep it (its bounds, no secret, nothing masking would hide).
package netconf

import (
	"fmt"
	"strings"
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
// (internal/engine's maxRecordedText, which a Collection cannot import): past
// it, the journal keeps none of the undo and a rollback refuses the task as
// not recorded in full, so a longer undo is not recorded at all, and the run
// says so.
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
	seen := map[string]bool{}
	for _, g := range p.deferred {
		key := groupKey(g)
		if seen[key] {
			continue
		}
		seen[key] = true
		ops, why := decideGroup(instancesAt(before, g), instancesAt(after, g))
		if why != "" {
			p.why = fmt.Sprintf("the edit changed %s in a way the reads cannot name for certain without the device's YANG schema: %s", g.name.Local, why)
			return
		}
		if len(ops) > 0 {
			p.top = place(p.top, g.path, ops...)
		}
	}
	p.deferred = nil
}

// groupKey names g once, for deduplication.
func groupKey(g group) string {
	key := ""
	for _, step := range g.path {
		key += "{" + step.Name.Space + "}" + step.Name.Local + "/"
	}
	return key + "{" + g.name.Space + "}" + g.name.Local
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
// instances before and after, each compared whole: nothing when it is the
// same; the removal of each instance that is new, when nothing that was there
// changed; one instance put back, when exactly one changed into exactly one
// other. Anything else is why there is none.
func decideGroup(before, after []*yangdoc.Element) ([]*yangdoc.Element, string) {
	gone := missingFrom(before, after)
	added := missingFrom(after, before)
	switch {
	case len(gone) == 0 && len(added) == 0:
		return nil, ""
	case len(gone) == 0 && len(after) == len(before)+len(added):
		out := make([]*yangdoc.Element, 0, len(added))
		for _, y := range added {
			out = append(out, removal(y, y.LeadingLeaves()))
		}
		return out, ""
	case len(gone) == 1 && len(added) == 1 && len(after) == len(before):
		return []*yangdoc.Element{restore(gone[0])}, ""
	}
	return nil, fmt.Sprintf("%d instances were there before and %d after, %d of them gone and %d new", len(before), len(after), len(gone), len(added))
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
// it is not one the run's journal may keep: an attribute it cannot write,
// longer than maxUndoBytes, holding text a terminal acts on, a template
// opening the method would read as a secret reference, a masked placeholder,
// or anything masking would hide (a secret-named leaf, a crypt-shaped value,
// the run's own credential). credentials are the credential's values. An
// undo putting something back is exact only from reads in with-defaults
// explicit mode (explicit): another mode reports a default value as set, or a
// value set to its default as absent, and putting back such a read would
// change which values are set.
func undoContent(p *plan, credentials []string, explicit bool) (string, string) {
	if len(p.top) == 0 {
		return "", ""
	}
	doc, err := yangdoc.Write(p.top)
	if err != nil {
		return "", "its undo cannot be written: " + err.Error()
	}
	switch {
	case !explicit && strings.Contains(doc, `nc:operation="`+opReplace+`"`):
		return "", "it would put back what the device read, and the device does not read in with-defaults explicit mode, so the read cannot tell a value set from a default"
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
		return "", "the configuration it would put back holds what masking hides (a secret), which the run's journal never keeps"
	}
	return doc, ""
}

// recordUndo records the undo of a run of req, whose content is undo: this
// method again with undo as its content, default_operation none, and the
// datastore the task named, if it named one. It stands even if the task
// fails, since running it where nothing changed changes nothing: it removes
// only what the read before found absent, and puts back only what it read.
func recordUndo(rc sdk.RunbookContext, req configRequest, undo string) error {
	params := map[string]any{paramContent: undo, paramDefaultOperation: netconf.DefaultOperationNone}
	if req.target != "" {
		params[paramDatastore] = string(req.target)
	}
	return sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:          configFQCN,
		Params:        params,
		Description:   "puts back what this run changed: " + undo,
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
