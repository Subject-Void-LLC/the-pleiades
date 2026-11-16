// Package gnmi: net.gnmi.config's undo: where each node a write creates
// starts, read before the Set, the bounds the run journal keeps an undo to,
// and the delete of what the Set created, recorded once it is applied.
package gnmi

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// createdRoots is, for each update or replace path the read before found
// absent, by path, where the node a write there would create starts: the
// highest node above it that the device holds nothing at, read now, while it
// is still absent. A read that fails refuses the write: an undo is recorded
// the moment the Set is applied, from these alone, and a write whose undo
// could not be decided would make a change the manifest promises to undo
// with none.
func createdRoots(ctx context.Context, client *gnmi.Client, req writeRequest, targets []gnmi.Path, before []reading) (map[string]gnmi.Path, error) {
	roots := map[string]gnmi.Path{}
	for _, v := range req.writes() {
		if before[indexOf(targets, v.path)].present {
			continue
		}
		root, err := createdRoot(ctx, client, v.path, req)
		if err != nil {
			return nil, fmt.Errorf("reading what is above %s, to know what an undo deletes, failed: %w", v.path, err)
		}
		roots[v.path.String()] = root
	}
	return roots, nil
}

// writes are the request's replaces, then its updates.
func (req writeRequest) writes() []value {
	return append(append([]value(nil), req.replaces...), req.updates...)
}

// createdRoot is the node a write at p would create: p itself, or the
// highest of its ancestors the device holds nothing at, read upward until one
// is present, so an undo deleting it removes only what the write made. Each
// read is made as req's reads are, in its encoding and against its target.
func createdRoot(ctx context.Context, client *gnmi.Client, p gnmi.Path, req writeRequest) (gnmi.Path, error) {
	root := p
	for n := len(p.Elems) - 1; n >= 1; n-- {
		above := gnmi.Path{Origin: p.Origin, Elems: p.Elems[:n]}
		r, err := read(ctx, client, above, req)
		if err != nil {
			return gnmi.Path{}, err
		}
		if r.present {
			break
		}
		root = above
	}
	return root, nil
}

// recordUndo records the delete of the node each update or replace path
// created, which is exact: for each path the read before found absent (it
// has a root) and keep, when given, accepts, its root, once each. A path that
// was there records none, and the run says why only through the method's
// documentation: putting its earlier value back would keep that value in the
// journal. Deleting a root a Set left absent changes nothing, so an undo
// recorded for a Set whose effect is not known yet is safe to keep.
func recordUndo(rc sdk.RunbookContext, req writeRequest, roots map[string]gnmi.Path, keep func(value) bool) error {
	names := undoDeletes(req, roots, keep)
	if len(names) == 0 {
		return nil
	}
	deletes := make([]any, len(names))
	for i, n := range names {
		deletes[i] = n
	}
	// The undo names the tree the write did, an empty target for none, since
	// a device's gnmi_target may have changed, or the task named its own, by
	// the time it runs.
	target := req.target
	if target == gnmi.NoPrefixTarget {
		target = ""
	}
	params := map[string]any{paramDelete: deletes, paramEncoding: req.encoding.String(), paramTarget: target}
	return sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:          configFQCN,
		Params:        params,
		Description:   "deletes " + strings.Join(names, ", ") + ", which this run created",
		KeepOnFailure: true,
	})
}

// The bounds the run journal holds an undo's values to (internal/engine's
// maxRecordedItems and maxRecordedText, which a Collection cannot import):
// past either, the journal keeps none of the undo, and a rollback refuses the
// task as not recorded in full, under a manifest saying the undo is exact.
const (
	// maxUndoDeletes is the most nodes one undo deletes.
	maxUndoDeletes = 128
	// maxUndoPathBytes bounds one node's path, as gNMI writes it.
	maxUndoPathBytes = 256
)

// undoFits refuses an undo deleting more nodes than the journal keeps, or a
// node whose path is longer than it keeps, before the Set that would need it
// is sent. Every other rule of the journal's (valid UTF-8, nothing a terminal
// acts on) already holds for a path, which Path.Validate checked.
func undoFits(deletes []string) error {
	if len(deletes) > maxUndoDeletes {
		return fmt.Errorf("the write creates %d nodes, and its undo, which deletes each, can name at most %d: split the task", len(deletes), maxUndoDeletes)
	}
	for _, d := range deletes {
		if len(d) > maxUndoPathBytes {
			return fmt.Errorf("the write creates a node whose path is %d bytes, and its undo can name one of at most %d: write it in a task whose path creates less", len(d), maxUndoPathBytes)
		}
	}
	return nil
}

// undoDeletes are the roots recordUndo deletes, as gNMI writes each path, in
// the request's order, once each.
func undoDeletes(req writeRequest, roots map[string]gnmi.Path, keep func(value) bool) []string {
	var names []string
	seen := map[string]bool{}
	for _, v := range req.writes() {
		root, ok := roots[v.path.String()]
		if !ok || (keep != nil && !keep(v)) || seen[root.String()] {
			continue
		}
		seen[root.String()] = true
		names = append(names, root.String())
	}
	return names
}
