// Package netconf: net.netconf.config's undo, planned from the configuration
// the edit filter read before the edit and the edit's own content.
//
// No YANG schema is at hand, so which element is a list entry and which of
// its leaves are its keys is never known; the plan rests only on what the
// reads prove. An element whose name the read before holds no instance of was
// created, and its undo removes it. An element the read names for certain (a
// container, which a list entry cannot be, since it carries no leaf; or the
// one instance agreeing with the content on every leading leaf it holds,
// where a list entry's keys are, RFC 7950 section 7.8.5) is recurred into,
// and where a leaf of it changes, or a part of it cannot be named, it is put
// back whole as it was read. What is decided this way is recorded before the
// edit is sent, so an edit whose answer is lost still leaves its undo.
//
// A content element the read cannot tell apart from what is there, a list
// entry whose leading leaves the edit changes, sitting directly under the
// containers a filter follows, is left to the read after the edit: the
// instances of its name before and after are compared, and one created or
// one changed is undone as such. Such an undo can be recorded only once the
// read after has answered, so a lost answer leaves none, and the run says
// so.
package netconf

import (
	"encoding/xml"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

// The operations edit-config's operation attribute takes (RFC 6241 section
// 7.2), and none, the default-operation that changes nothing.
const (
	opMerge   = "merge"
	opReplace = "replace"
	opCreate  = "create"
	opDelete  = "delete"
	opRemove  = "remove"
	opNone    = "none"
)

// operationAttr is an undo element's operation attribute, nc:operation.
func operationAttr(op string) []xml.Attr {
	return []xml.Attr{{Name: xml.Name{Space: yangdoc.NetconfBase, Local: "operation"}, Value: op}}
}

// group is the instances of one name under the containers a filter followed
// to it, the unit a deferred undo is decided on.
type group struct {
	// path is the containers above the group, outermost first, each as the
	// content names it.
	path []*yangdoc.Element
	// name is the group's element name.
	name xml.Name
}

// plan is an undo being planned.
type plan struct {
	// top is the undo document so far: its top-level elements.
	top []*yangdoc.Element
	// deferred are the groups left to the read after the edit.
	deferred []group
	// why is the first reason no undo can be planned; "" while one can.
	why string
}

// planUndo is the undo of content (parsed) against before, what the edit
// filter read before the edit, with defaultOp edit-config's
// default-operation.
func planUndo(content, before []*yangdoc.Element, defaultOp string) *plan {
	p := &plan{}
	if defaultOp == netconf.DefaultOperationReplace {
		p.why = "default_operation replace replaces the whole datastore, whose earlier contents the run's journal cannot keep"
		return p
	}
	if defaultOp == "" {
		defaultOp = opMerge
	}
	for _, e := range content {
		p.container(e, before, nil, defaultOp)
		if p.why != "" {
			return p
		}
	}
	return p
}

// container plans e, at a level the filter followed by name, against
// device, the instances the read holds at this level. A container is
// followed into; the first element that may be anything else is planned as
// a group, every instance of its name read whole.
func (p *plan) container(e *yangdoc.Element, device []*yangdoc.Element, path []*yangdoc.Element, op string) {
	if !netconf.Descendable(e) {
		p.group(e, yangdoc.FindIn(device, e.Name), path, op)
		return
	}
	found := yangdoc.FindIn(device, e.Name)
	if len(found) > 1 {
		p.why = fmt.Sprintf("the read holds %d instances of %s, which the content names as a container", len(found), e.Name.Local)
		return
	}
	var below []*yangdoc.Element
	if len(found) == 1 {
		below = found[0].Children
	}
	inner := extend(path, e.Copy(nil))
	for _, c := range e.Children {
		p.container(c, below, inner, op)
		if p.why != "" {
			return
		}
	}
}

// group plans e against instances, every instance of its name under path,
// each read whole. An element no instance can be named for is deferred to the
// read after the edit.
func (p *plan) group(e *yangdoc.Element, instances []*yangdoc.Element, path []*yangdoc.Element, op string) {
	steps := extend(nil, path...)
	ops, ok := planEntry(e, instances, op)
	switch {
	case !ok && e.IsLeaf():
		p.why = "the edit changes " + e.Name.Local + ", a value directly under containers, which no read can tell a leaf from a list of leaves for"
	case !ok:
		p.deferred = append(p.deferred, group{path: steps, name: e.Name})
	case ops != nil:
		p.top = place(p.top, steps, ops...)
	}
}

// planEntry is the undo of content element e against instances, the
// device's instances of its name, each read whole: the elements of the undo
// at e's level, none when e changes nothing, and false when e cannot be named
// for certain. op is the operation e inherits.
func planEntry(e *yangdoc.Element, instances []*yangdoc.Element, op string) ([]*yangdoc.Element, bool) {
	if len(e.Attrs) > 0 {
		own := netconf.Operation(e)
		if own == "" || len(e.Attrs) > 1 {
			// An attribute other than the operation (yang:insert's position)
			// is something these reads cannot account for.
			return nil, false
		}
		op = own
	}
	if op == opNone && !holdsOperation(e) {
		// RFC 6241 section 7.2: under none the datastore is unaffected
		// until an operation attribute says otherwise, and none does here.
		return nil, true
	}
	if e.IsLeaf() {
		return planLeaf(e, instances, op)
	}
	x, absent, ok := match(e, instances)
	switch {
	case !ok:
		return nil, false
	case absent && (op == opDelete || op == opRemove):
		// Nothing there to take away: a delete of it fails the edit, and a
		// remove of it changes nothing.
		return nil, true
	case absent:
		return []*yangdoc.Element{removal(e, allLeaves(e))}, true
	case op == opDelete || op == opRemove || op == opReplace:
		return []*yangdoc.Element{restore(x)}, true
	case op == opCreate:
		// A create of what is there fails the edit and changes nothing.
		return nil, true
	}
	// A merge, or none: each part of e against x, x put back whole when a
	// part cannot be planned.
	inner := x.Copy(nil, x.LeadingLeaves()...)
	for _, c := range e.Children {
		ops, ok := planEntry(c, x.Find(c.Name), op)
		if !ok {
			return []*yangdoc.Element{restore(x)}, true
		}
		inner.Children = append(inner.Children, ops...)
	}
	if len(inner.Children) == len(x.LeadingLeaves()) {
		return nil, true
	}
	return []*yangdoc.Element{inner}, true
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

// planLeaf is the undo of a leaf (or a leaf-list entry) e against instances:
// nothing when its value is there or the operation changes nothing, its
// removal when no instance holds it and either none is there or two or more
// are (a leaf is never there twice, so those are a leaf-list's), and false
// when one instance of another value is there, which is a changed leaf or a
// leaf-list entry added beside it.
func planLeaf(e *yangdoc.Element, instances []*yangdoc.Element, op string) ([]*yangdoc.Element, bool) {
	if op == opDelete || op == opRemove || op == opReplace {
		return nil, false
	}
	for _, x := range instances {
		if yangdoc.CanonicalValue(x) == yangdoc.CanonicalValue(e) {
			return nil, true
		}
	}
	if len(instances) == 1 {
		return nil, false
	}
	return []*yangdoc.Element{removal(e, nil)}, true
}

// match finds e's instance among instances: absent when there is certainly
// none, and false when it cannot be told. e holds children.
func match(e *yangdoc.Element, instances []*yangdoc.Element) (x *yangdoc.Element, absent, ok bool) {
	if len(instances) == 0 {
		return nil, true, true
	}
	if len(allLeaves(e)) == 0 {
		// e carries no leaf, so no key: it can only be a container.
		if len(instances) == 1 {
			return instances[0], false, true
		}
		return nil, false, false
	}
	if len(instances) == 1 {
		lead := instances[0].LeadingLeaves()
		if len(lead) == 0 || len(yangdoc.FindIn(e.Children, lead[0].Name)) == 0 {
			// The instance starts with no leaf, so it is no list entry; or e
			// lacks the leaf it starts with, which as a list entry's first key
			// e would have to carry: either way a container.
			return instances[0], false, true
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
		return agree[0], false, true
	case len(agree) > 1:
		return nil, false, false
	case len(instances) >= 2 && firstKeyNew(e, instances):
		return nil, true, true
	}
	return nil, false, false
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
	var first xml.Name
	for i, x := range instances {
		lead := x.LeadingLeaves()
		if len(lead) == 0 || (i > 0 && lead[0].Name != first) {
			return false
		}
		first = lead[0].Name
	}
	mine := yangdoc.FindIn(e.Children, first)
	if len(mine) != 1 || !mine[0].IsLeaf() {
		return false
	}
	for _, x := range instances {
		if yangdoc.CanonicalValue(x.LeadingLeaves()[0]) == yangdoc.CanonicalValue(mine[0]) {
			return false
		}
	}
	return true
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

// removal is the undo element removing e, named by ident (its leaves, a
// list entry's keys among them), or by its value for a leaf.
func removal(e *yangdoc.Element, ident []*yangdoc.Element) *yangdoc.Element {
	return e.Copy(operationAttr(opRemove), ident...)
}

// restore is the undo element putting x back whole as it was read.
func restore(x *yangdoc.Element) *yangdoc.Element {
	return x.Copy(operationAttr(opReplace), x.Children...)
}

// extend is path with more appended, in a new slice.
func extend(path []*yangdoc.Element, more ...*yangdoc.Element) []*yangdoc.Element {
	return append(append(make([]*yangdoc.Element, 0, len(path)+len(more)), path...), more...)
}
