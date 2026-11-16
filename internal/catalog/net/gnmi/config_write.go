// Package gnmi: net.gnmi.config's write itself: the reads before, the one
// Set, the reads after, the undo where it is exact, and what is recorded.
package gnmi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// readBackTimeout bounds the reads after a Set whose answer was lost, which
// may run after the task's own deadline.
const readBackTimeout = 30 * time.Second

// reading is one path's read: its nodes and values as canonical JSON, by
// full path, and whether anything was there.
type reading struct {
	doc     json.RawMessage
	present bool
}

// write reads every path, sends the Set, records the undo of each node the
// Set created as soon as the Set is known to be applied, reads every path
// again, and records the backup and the diff, returning the warnings the task
// should carry. Once the Set may have been applied, no later failure reports
// the task unchanged or drops its undo.
func write(ctx context.Context, rc sdk.RunbookContext, client *gnmi.Client, req writeRequest) (collection.Result, []string, error) {
	var notes []string
	// The secret references are resolved first, before anything is read, so
	// each value they stand for is in the client's masking before any read
	// can meet it: a run after the one that wrote a secret reads it back,
	// under whatever leaf holds it (a description is no secret-named field),
	// into the backup, the diff and any error the device words.
	set, err := req.set(rc, client)
	if err != nil {
		return collection.Result{}, nil, fmt.Errorf("%w; nothing was sent", err)
	}
	targets := req.targets()
	before := make([]reading, len(targets))
	for i, p := range targets {
		r, err := read(ctx, client, p, req)
		if err != nil {
			return collection.Result{}, nil, fmt.Errorf("reading %s before writing: %w; nothing was written", p, err)
		}
		before[i] = r
	}
	roots, err := createdRoots(ctx, client, req, targets, before)
	if err != nil {
		return collection.Result{}, nil, fmt.Errorf("%w; nothing was written", err)
	}
	if err := undoFits(undoDeletes(req, roots, nil)); err != nil {
		return collection.Result{}, nil, fmt.Errorf("%w; nothing was sent", err)
	}
	backups, backupErr := recordedAll(rc, client, targets, before)
	if req.backup {
		if backupErr != nil {
			return collection.Result{}, nil, fmt.Errorf("the backup is not recorded (%w); nothing was written", backupErr)
		}
		if err := rc.SetStat(statBackup, backups); err != nil {
			return collection.Result{}, nil, err
		}
	}
	if len(req.replaces)+len(req.updates) == 0 && !anyPresent(before) {
		notes = append(notes, "every path to delete is not there, so nothing was sent")
		return collection.Result{Changed: false}, notes, recordDiff(rc, targets, before, backups, backupErr, before, backups, backupErr)
	}
	_, setErr := client.Set(ctx, set)
	var lost *gnmi.AnswerLostError
	if setErr != nil && !errors.As(setErr, &lost) {
		// gNMI applies all of a Set or none of it, so a refused one changed
		// nothing.
		return collection.Result{}, notes, fmt.Errorf("the device refused the Set, and gNMI applies all of a Set or none, so nothing changed: %w", setErr)
	}
	// An OK answer, read or not, is a Set the device applied whole: its undo
	// is recorded now, from the reads before alone, before anything else can
	// fail. A lost answer's is recorded once the read after says what it did.
	applied := setErr == nil || lost.Applied
	if applied {
		if err := recordUndo(rc, req, roots, nil); err != nil {
			return collection.Result{Changed: true}, notes, fmt.Errorf("the Set was applied, and its undo could not be recorded: %w", err)
		}
	}
	// A lost answer may be the caller's deadline passing, so the read that
	// says what happened gets its own bound rather than none.
	readCtx := ctx
	if setErr != nil {
		var cancel context.CancelFunc
		readCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), readBackTimeout)
		defer cancel()
	}
	after := make([]reading, len(targets))
	for i, p := range targets {
		r, err := read(readCtx, client, p, req)
		if err != nil {
			why := fmt.Errorf("reading %s after the Set failed: %w", p, err)
			if setErr != nil {
				why = fmt.Errorf("%w, and %w", setErr, why)
			}
			if !applied {
				// Whether the lost Set was applied is not known, so the undo
				// is kept for the change it may have made: it deletes only
				// nodes that were absent before, which a Set never applied
				// leaves absent, so running it then changes nothing.
				if uerr := recordUndo(rc, req, roots, nil); uerr != nil {
					why = fmt.Errorf("%w, and its undo could not be recorded: %w", why, uerr)
				}
			}
			return collection.Result{Changed: true}, notes, why
		}
		after[i] = r
	}
	if !applied {
		// A lost answer read back: the undo deletes what is there now and
		// was absent before.
		created := func(v value) bool { return after[indexOf(targets, v.path)].present }
		if err := recordUndo(rc, req, roots, created); err != nil {
			return collection.Result{Changed: true}, notes, fmt.Errorf("%w; its undo could not be recorded: %w", setErr, err)
		}
	}
	changed := false
	for i := range targets {
		if before[i].present != after[i].present || string(before[i].doc) != string(after[i].doc) {
			changed = true
		}
	}
	afterDocs, afterErr := recordedAll(rc, client, targets, after)
	if err := recordDiff(rc, targets, before, backups, backupErr, after, afterDocs, afterErr); err != nil {
		return collection.Result{Changed: changed}, notes, err
	}
	if setErr != nil {
		var what string
		switch {
		case lost.Applied:
			what = "the device applied it"
		case changed:
			what = "the paths read differently than before, so it was applied"
		default:
			what = "the paths read the same as before, so it was most likely not applied"
		}
		return collection.Result{Changed: changed}, notes, fmt.Errorf("%w; read back, %s", setErr, what)
	}
	return collection.Result{Changed: changed}, notes, nil
}

// targets are every path the request names, once each: deletes, then
// replaces, then updates.
func (req writeRequest) targets() []gnmi.Path {
	var out []gnmi.Path
	add := func(p gnmi.Path) {
		if indexOf(out, p) < 0 {
			out = append(out, p)
		}
	}
	for _, p := range req.deletes {
		add(p)
	}
	for _, v := range req.replaces {
		add(v.path)
	}
	for _, v := range req.updates {
		add(v.path)
	}
	return out
}

// set is the request on the wire, each value's secret references resolved.
func (req writeRequest) set(rc sdk.RunbookContext, client *gnmi.Client) (gnmi.SetRequest, error) {
	out := gnmi.SetRequest{Delete: req.deletes, PrefixTarget: req.target}
	for _, list := range []struct {
		in  []value
		out *[]gnmi.SetValue
	}{{req.replaces, &out.Replace}, {req.updates, &out.Update}} {
		for _, v := range list.in {
			doc, err := resolve(rc, client, v.val)
			if err != nil {
				return gnmi.SetRequest{}, fmt.Errorf("the value at %s: %w", v.path, err)
			}
			*list.out = append(*list.out, gnmi.SetValue{Path: v.path, JSON: doc, Encoding: req.encoding})
		}
	}
	return out, nil
}

// indexOf is p's index in paths, or -1.
func indexOf(paths []gnmi.Path, p gnmi.Path) int {
	for i, q := range paths {
		if q.Equal(p) {
			return i
		}
	}
	return -1
}

// anyPresent reports whether any read found something.
func anyPresent(rs []reading) bool {
	for _, r := range rs {
		if r.present {
			return true
		}
	}
	return false
}

// read reads p's configuration in req's encoding, against req's target
// (the client's when it names none): absent for the device's NOT_FOUND and
// for an answer holding no value, else every node it answered by its full
// path, as one canonical JSON object.
func read(ctx context.Context, client *gnmi.Client, p gnmi.Path, req writeRequest) (reading, error) {
	ns, err := client.Get(ctx, gnmi.GetRequest{Paths: []gnmi.Path{p}, Encoding: req.encoding, Type: gnmi.DataConfig, PrefixTarget: req.target})
	if gnmi.Code(err) == codes.NotFound {
		return reading{}, nil
	}
	if err != nil {
		return reading{}, err
	}
	nodes := map[string]json.RawMessage{}
	for _, n := range ns {
		for _, u := range n.Updates {
			if u.Value.Type == gnmi.TypeNone {
				continue
			}
			nodes[gnmi.Join(n.Prefix, u.Path).String()] = u.Value.JSON
		}
	}
	if len(nodes) == 0 {
		return reading{}, nil
	}
	doc, err := json.Marshal(nodes)
	if err != nil {
		return reading{}, err
	}
	return reading{doc: doc, present: true}, nil
}

// recordedAll is each path's reading as a run may keep it, by path:
// masked by value (the device's credential and every named secret a value
// resolved) and then by shape (pkg/netcfg), each secret the shape masking
// found joining the run's masking set; nil for a path that was not there.
func recordedAll(rc sdk.RunbookContext, client *gnmi.Client, targets []gnmi.Path, rs []reading) (map[string]any, error) {
	out := make(map[string]any, len(targets))
	for i, p := range targets {
		if !rs[i].present {
			out[p.String()] = nil
			continue
		}
		byValue, err := client.Mask(string(rs[i].doc))
		if err != nil {
			return nil, err
		}
		masked, found, err := netcfg.MaskOutput(byValue)
		if err != nil {
			return nil, err
		}
		sdk.RegisterSecret(rc, netcfg.Learnable(found)...)
		var doc any
		if err := json.Unmarshal([]byte(masked), &doc); err != nil {
			return nil, fmt.Errorf("the masked reading of %s is no longer JSON: %w", p, err)
		}
		out[p.String()] = doc
	}
	return out, nil
}

// recordDiff records every path before and after, masked, or withheld when
// masking could not vouch for a side.
func recordDiff(rc sdk.RunbookContext, targets []gnmi.Path, before []reading, beforeDocs map[string]any, beforeErr error, after []reading, afterDocs map[string]any, afterErr error) error {
	side := func(rs []reading, docs map[string]any, err error) map[string]any {
		out := make(map[string]any, len(targets))
		for i, p := range targets {
			entry := map[string]any{"exists": rs[i].present}
			switch {
			case err != nil:
				entry["value"] = "(withheld: masking cannot vouch for it)"
			case rs[i].present:
				entry["value"] = docs[p.String()]
			}
			out[p.String()] = entry
		}
		return out
	}
	return sdk.RecordDiff(rc, sdk.Diff{Before: side(before, beforeDocs, beforeErr), After: side(after, afterDocs, afterErr)})
}
