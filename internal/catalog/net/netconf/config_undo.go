// Package netconf: net.netconf.config's undo, planned from the configuration
// the edit filter read before the edit and the edit's own content.
//
// No YANG schema is at hand, so which element is a list entry and which of
// its leaves are its keys is never known; the plan rests only on what the
// reads prove. An element whose name the read before holds no instance of was
// created, and its undo removes it. An element the read names for certain (a
// container, which a list entry cannot be, since it carries no leaf; or the
// one instance agreeing with the content on every leading leaf it holds,
// where a list entry's keys are, RFC 7950 section 7.8.5) is recurred into.
//
// Only removals are journaled, each built from the author's own content (its
// element names, the keys it wrote, its own values), never from text read off
// the device (pkg/sdk's InverseSpec). A change to something that was there
// would be undone only by putting back what the device held, so it records no
// journaled undo, and the run says so.
//
// The groups under the containers a filter follows are planned whole: when
// one content element of a group cannot be told apart from what is there (a
// list entry whose leading leaves the edit changes), the whole group is left
// to the read after the edit, which attributes each new instance to a content
// element of its own. That undo is recorded only once the read after has
// answered, so a lost answer leaves none, and the run says so.
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

// whyPutBack is why a change to something that was there records no
// journaled undo.
const whyPutBack = "it changes configuration that was there, whose undo would put back what was read from the device, and the run's journal keeps no configuration read from a device (only removals of what this task's own content created are recorded); a rollback refuses this task unless it is left (--leave) or the task's rollback: list says what to do"

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
	// content are the content's elements of that name there.
	content []*yangdoc.Element
}

// plan is an undo being planned.
type plan struct {
	// top is the undo document so far: its top-level elements.
	top []*yangdoc.Element
	// deferred are the groups left to the read after the edit.
	deferred []group
	// why is the first reason no undo can be recorded; "" while one can.
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
	p.level(content, before, nil, defaultOp)
	return p
}

// level plans els, the content's elements at a level the filter followed by
// name, against device, what the read holds there. Elements of one name are
// planned together: containers are followed into, and any other name is a
// group, every instance of it read whole.
func (p *plan) level(els, device []*yangdoc.Element, path []*yangdoc.Element, op string) {
	for _, name := range names(els) {
		same := yangdoc.FindIn(els, name)
		found := yangdoc.FindIn(device, name)
		if !allDescendable(same) {
			p.group(group{path: extend(nil, path...), name: name, content: same}, found, op)
		} else if len(found) > 1 {
			p.why = fmt.Sprintf("the read holds %d instances of %s, which the content names as a container", len(found), name.Local)
		} else {
			var below []*yangdoc.Element
			if len(found) == 1 {
				below = found[0].Children
			}
			var kids []*yangdoc.Element
			for _, e := range same {
				kids = append(kids, e.Children...)
			}
			p.level(kids, below, extend(path, same[0].Copy(nil)), op)
		}
		if p.why != "" {
			return
		}
	}
}

// group plans every content element of g against instances. One that cannot
// be named for certain leaves the whole group to the read after the edit, so
// nothing is planned twice for it; one whose undo is a put-back stops the
// plan.
func (p *plan) group(g group, instances []*yangdoc.Element, op string) {
	var ops []*yangdoc.Element
	for _, e := range g.content {
		got, v := planEntry(e, instances, op)
		switch {
		case v == ambiguous && e.IsLeaf():
			p.why = "the edit changes " + e.Name.Local + ", a value directly under containers, which no read can tell a leaf from a list of leaves for"
			return
		case v == ambiguous:
			p.deferred = append(p.deferred, g)
			return
		case v == putBack:
			p.why = whyPutBack
			return
		}
		ops = append(ops, got...)
	}
	if len(ops) > 0 {
		p.top = place(p.top, g.path, ops...)
	}
}

// names are the distinct names of els, in the order they first appear.
func names(els []*yangdoc.Element) []xml.Name {
	seen := map[xml.Name]bool{}
	var out []xml.Name
	for _, e := range els {
		if !seen[e.Name] {
			seen[e.Name] = true
			out = append(out, e.Name)
		}
	}
	return out
}

// allDescendable reports whether every element of els can only be a
// container (netconf.Descendable).
func allDescendable(els []*yangdoc.Element) bool {
	for _, e := range els {
		if !netconf.Descendable(e) {
			return false
		}
	}
	return true
}

// extend is path with more appended, in a new slice.
func extend(path []*yangdoc.Element, more ...*yangdoc.Element) []*yangdoc.Element {
	return append(append(make([]*yangdoc.Element, 0, len(path)+len(more)), path...), more...)
}
