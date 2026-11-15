// Package restconf: net.restconf.config's write itself: the read before, the
// write, the undo where it is exact, the read after, and what is recorded.
package restconf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// statBackup is the stat backup: true records the earlier document under.
const statBackup = "backup"

// write reads the resource, resolves the content, writes it, records the
// undo when the write created the resource, reads it again, and records the
// backup and the diff, returning the warnings the task should carry. For a
// post, path is the parent: the resource read after is the one its answer's
// Location names.
func write(ctx context.Context, rc sdk.RunbookContext, client *restconf.Client, req writeRequest) (collection.Result, []string, error) {
	var notes []string
	var before []byte
	had := false
	if req.method != restconf.MethodPost {
		var err error
		if before, had, err = client.Read(ctx, req.datastore, req.path, req.mediaType); err != nil {
			return collection.Result{}, nil, fmt.Errorf("reading the resource before writing: %w", err)
		}
	}
	maskedBefore, maskErr := recorded(rc, client, before)
	if req.backup {
		if maskErr != nil {
			return collection.Result{}, nil, fmt.Errorf("the backup is not recorded (%w); nothing was written", maskErr)
		}
		if err := rc.SetStat(statBackup, maskedBefore); err != nil {
			return collection.Result{}, nil, err
		}
	}
	switch {
	case req.method == restconf.MethodDelete && !had:
		notes = append(notes, "the resource is not there, so nothing was sent")
		return collection.Result{Changed: false}, notes, recordDiff(rc, had, maskedBefore, maskErr, had, maskedBefore, maskErr)
	case req.method == restconf.MethodPatch && !had:
		return collection.Result{}, nil, errors.New("patch merges into a resource that is there, and path names none: use put or post to create it; nothing was sent")
	}
	content, err := resolveContent(rc, client, req.content, req.asJSON)
	if err != nil {
		return collection.Result{}, nil, fmt.Errorf("%w; nothing was sent", err)
	}
	var body []byte
	if req.method != restconf.MethodDelete {
		body = []byte(content)
	}
	written, err := client.Write(ctx, req.method, req.datastore, req.path, req.mediaType, body)
	if err != nil {
		var status *restconf.StatusError
		// RFC 8040 section 4.4.1 answers a post of a resource that is there
		// with 409 and resource-denied (rousette does, measured
		// 2026-11-15); section 7 maps data-exists to 409 as well.
		if req.method == restconf.MethodPost && errors.As(err, &status) && status.Status == http.StatusConflict &&
			(status.HasTag("resource-denied") || status.HasTag("data-exists")) {
			notes = append(notes, "the resource post would create is already there, and post never replaces one, so nothing changed: use put to make it match content")
			return collection.Result{Changed: false}, notes, nil
		}
		return collection.Result{}, nil, err
	}
	readAt, created := req.path, req.method == restconf.MethodPut && !had
	if req.method == restconf.MethodPost {
		readAt, created = written.Location, written.Location != nil
		if !created {
			notes = append(notes, "the device named no Location this client could read for the resource post created, so no undo is recorded and nothing was read back")
		}
	}
	if created {
		// Recorded before the read after, so it stands if that read fails:
		// the resource is on the device either way.
		if err := recordUndo(rc, readAt, req); err != nil {
			return collection.Result{}, notes, err
		}
	}
	if readAt == nil {
		return collection.Result{Changed: true}, notes, recordDiff(rc, false, "", nil, true, "", nil)
	}
	after, present, err := client.Read(ctx, req.datastore, readAt, req.mediaType)
	if err != nil {
		return collection.Result{Changed: true}, notes, fmt.Errorf("the write was answered, and reading the resource after it failed: %w", err)
	}
	maskedAfter, afterErr := recorded(rc, client, after)
	changed := req.method == restconf.MethodPost || had != present || differ(before, after, req.asJSON, &notes)
	if created && !present {
		notes = append(notes, "the device answered the write, and the resource it created is not there to read")
	}
	return collection.Result{Changed: changed}, notes, recordDiff(rc, had, maskedBefore, maskErr, present, maskedAfter, afterErr)
}

// differ reports whether two reads of a resource differ in their canonical
// form; when either cannot be read as one, it compares the bytes and says
// so.
func differ(before, after []byte, asJSON bool, notes *[]string) bool {
	a, errA := canonical(before, asJSON)
	b, errB := canonical(after, asJSON)
	if errA != nil || errB != nil {
		*notes = append(*notes, "the device's answer could not be read as one document, so the reads before and after were compared byte for byte")
		return !bytes.Equal(bytes.TrimSpace(before), bytes.TrimSpace(after))
	}
	return a != b
}

// recorded is a device's answer as a run may keep it: masked by value (the
// device's credential and every named secret the content resolved) and then
// by shape (pkg/netcfg), each secret the shape masking found joining the
// run's masking set. Nothing ("") for no answer.
func recorded(rc sdk.RunbookContext, client *restconf.Client, body []byte) (string, error) {
	if len(body) == 0 {
		return "", nil
	}
	masked, found, err := netcfg.MaskOutput(client.Mask(string(body)))
	if err != nil {
		return "", err
	}
	sdk.RegisterSecret(rc, netcfg.Learnable(found)...)
	return masked, nil
}

// recordDiff records the resource before and after, masked, or withheld
// when masking could not vouch for a side.
func recordDiff(rc sdk.RunbookContext, had bool, before string, beforeErr error, present bool, after string, afterErr error) error {
	side := func(exists bool, doc string, err error) map[string]any {
		out := map[string]any{"exists": exists}
		switch {
		case err != nil:
			out["document"] = "(withheld: masking cannot vouch for it)"
		case exists:
			out["document"] = doc
		}
		return out
	}
	return sdk.RecordDiff(rc, sdk.Diff{Before: side(had, before, beforeErr), After: side(present, after, afterErr)})
}

// recordUndo records the exact undo of a create: a delete of the resource
// created, by its path alone (and the format and datastore it was written
// in), which the journal may keep: none of the document is in it.
func recordUndo(rc sdk.RunbookContext, created restconf.Path, req writeRequest) error {
	escaped, err := created.Escaped()
	if err != nil {
		return err
	}
	params := map[string]any{paramPath: escaped, paramMethod: "delete", paramFormat: formatOf(req)}
	if req.datastore != "" {
		params[paramDatastore] = req.datastore
	}
	return sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:          configFQCN,
		Params:        params,
		Description:   "deletes " + escaped + ", which this run created",
		KeepOnFailure: true,
	})
}

// formatOf is the request's format parameter as written.
func formatOf(req writeRequest) string {
	if req.asJSON {
		return "json"
	}
	return "xml"
}
