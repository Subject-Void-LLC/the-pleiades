// Package netconf: one content element of an edit planned against the
// instances of its name the read before holds: whether the read names its
// instance for certain, and what its undo is.
package netconf

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

// verdict is what planning one content element concluded.
type verdict int

const (
	// certain: the undo is the elements returned (none when nothing changes).
	certain verdict = iota
	// putBack: an exact undo would put back what the device held, which the
	// run's journal does not keep.
	putBack
	// ambiguous: the read cannot name the element's instance for certain.
	ambiguous
)

// planEntry is the undo of content element e against instances, the
// device's instances of its name, each read whole: the elements of the undo
// at e's level, built from e itself. op is the operation e inherits.
func planEntry(e *yangdoc.Element, instances []*yangdoc.Element, op string) ([]*yangdoc.Element, verdict) {
	if len(e.Attrs) > 0 {
		own := netconf.Operation(e)
		if own == "" || len(e.Attrs) > 1 {
			// An attribute other than the operation (yang:insert's position)
			// is something these reads cannot account for.
			return nil, ambiguous
		}
		op = own
	}
	if op == opNone && !holdsOperation(e) {
		// RFC 6241 section 7.2: under none the datastore is unaffected
		// until an operation attribute says otherwise, and none does here.
		return nil, certain
	}
	if e.IsLeaf() {
		return planLeaf(e, instances, op)
	}
	x, absent, container, ok := match(e, instances)
	switch {
	case !ok:
		return nil, ambiguous
	case absent && (op == opDelete || op == opRemove):
		// Nothing there to take away: a delete of it fails the edit, and a
		// remove of it changes nothing.
		return nil, certain
	case absent:
		return []*yangdoc.Element{removal(e)}, certain
	case op == opDelete || op == opRemove || op == opReplace:
		return nil, putBack
	case op == opCreate:
		// A create of what is there fails the edit and changes nothing.
		return nil, certain
	}
	// A merge, or none: each part of e against x. A part that cannot be named
	// would be undone only by putting x back.
	step := e.Copy(nil, identity(e, x, container)...)
	idents := len(step.Children)
	for _, c := range e.Children {
		ops, v := planEntry(c, x.Find(c.Name), op)
		switch v {
		case ambiguous, putBack:
			return nil, putBack
		}
		step.Children = append(step.Children, ops...)
	}
	if len(step.Children) == idents {
		return nil, certain
	}
	return []*yangdoc.Element{step}, certain
}

// planLeaf is the undo of a leaf (or a leaf-list entry) e against instances:
// nothing when its value is there or a delete or remove finds none to take;
// its removal when no instance holds it and either none is there or two or
// more are (a leaf is never there twice, so those are a leaf-list's); a
// put-back for a delete or remove of a value that is there; and ambiguous when
// one instance of another value is there, which is a changed leaf or a
// leaf-list entry added beside it.
func planLeaf(e *yangdoc.Element, instances []*yangdoc.Element, op string) ([]*yangdoc.Element, verdict) {
	there := false
	for _, x := range instances {
		if yangdoc.CanonicalValue(x) == yangdoc.CanonicalValue(e) {
			there = true
		}
	}
	switch {
	case (op == opDelete || op == opRemove) && there:
		return nil, putBack
	case op == opDelete || op == opRemove:
		return nil, certain
	case there:
		return nil, certain
	case len(instances) == 1:
		return nil, ambiguous
	}
	return []*yangdoc.Element{removal(e)}, certain
}

// match finds e's instance among instances: absent when there is certainly
// none, container when the reads prove it one (so its name alone names it),
// and false when it cannot be told. e holds children.
func match(e *yangdoc.Element, instances []*yangdoc.Element) (x *yangdoc.Element, absent, container, ok bool) {
	if len(instances) == 0 {
		return nil, true, false, true
	}
	if len(allLeaves(e)) == 0 {
		// e carries no leaf, so no key: it can only be a container.
		if len(instances) == 1 {
			return instances[0], false, true, true
		}
		return nil, false, false, false
	}
	if len(instances) == 1 {
		lead := instances[0].LeadingLeaves()
		if len(lead) == 0 || len(yangdoc.FindIn(e.Children, lead[0].Name)) == 0 {
			// The instance starts with no leaf, so it is no list entry; or e
			// lacks the leaf it starts with, which as a list entry's first key
			// e would have to carry: either way a container.
			return instances[0], false, true, true
		}
	}
	var agree []*yangdoc.Element
	for _, x := range instances {
		if agrees(e, x) {
			agree = append(agree, x)
		}
	}
	switch {
	case len(agree) == 1:
		return agree[0], false, false, true
	case len(agree) > 1:
		return nil, false, false, false
	case len(instances) >= 2 && firstKeyNew(e, instances):
		return nil, true, false, true
	}
	return nil, false, false, false
}

// agrees reports whether e holds, for every leading leaf of x it names, the
// value x has, x's keys among them. A name x or e holds more than once is a
// leaf-list's, never a key, and is passed over.
func agrees(e, x *yangdoc.Element) bool {
	lead := x.LeadingLeaves()
	for _, l := range lead {
		mine := yangdoc.FindIn(e.Children, l.Name)
		if len(mine) == 0 || len(mine) > 1 && mine[0].IsLeaf() || len(yangdoc.FindIn(lead, l.Name)) > 1 {
			continue
		}
		if len(mine) > 1 || !mine[0].IsLeaf() || yangdoc.CanonicalValue(mine[0]) != yangdoc.CanonicalValue(l) {
			return false
		}
	}
	return true
}

// firstKeyNew reports, for a list (two or more instances, so not a
// container), whether every instance starts with a leaf of one name, e holds
// it, and no instance holds e's value there: the first key is new, so the
// entry is.
func firstKeyNew(e *yangdoc.Element, instances []*yangdoc.Element) bool {
	var first []*yangdoc.Element
	for i, x := range instances {
		lead := x.LeadingLeaves()
		if len(lead) == 0 || (i > 0 && lead[0].Name != first[0].Name) {
			return false
		}
		first = append(first, lead[0])
	}
	mine := yangdoc.FindIn(e.Children, first[0].Name)
	if len(mine) != 1 || !mine[0].IsLeaf() {
		return false
	}
	for _, f := range first {
		if yangdoc.CanonicalValue(f) == yangdoc.CanonicalValue(mine[0]) {
			return false
		}
	}
	return true
}

// identity is what names e's instance x in an undo, from e's own leaves: none
// for a container, and for a list entry e's leaves of the names x's leading
// leaves hold once (a leaf-list's entries are never keys), x's keys among
// them, which e agrees with x on. Nothing here is text read off the device.
func identity(e, x *yangdoc.Element, container bool) []*yangdoc.Element {
	if container {
		return nil
	}
	lead := x.LeadingLeaves()
	keyName := map[string]bool{}
	for _, l := range lead {
		if len(yangdoc.FindIn(lead, l.Name)) == 1 {
			keyName["{"+l.Name.Space+"}"+l.Name.Local] = true
		}
	}
	var out []*yangdoc.Element
	for _, c := range allLeaves(e) {
		if keyName["{"+c.Name.Space+"}"+c.Name.Local] {
			out = append(out, c)
		}
	}
	return out
}

// allLeaves are e's children with no children of their own, in order.
func allLeaves(e *yangdoc.Element) []*yangdoc.Element {
	var out []*yangdoc.Element
	for _, c := range e.Children {
		if c.IsLeaf() {
			out = append(out, c)
		}
	}
	return out
}

// holdsOperation reports whether e or anything inside it carries an
// operation attribute.
func holdsOperation(e *yangdoc.Element) bool {
	if netconf.Operation(e) != "" {
		return true
	}
	for _, c := range e.Children {
		if holdsOperation(c) {
			return true
		}
	}
	return false
}

// removal is the undo element removing what content element e created: e
// named by its own leaves (a list entry's keys among them, wherever the
// author wrote them), or by its value for a leaf.
func removal(e *yangdoc.Element) *yangdoc.Element {
	return e.Copy(operationAttr(opRemove), allLeaves(e)...)
}
