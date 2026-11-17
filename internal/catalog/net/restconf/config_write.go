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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

// statBackup is the stat backup: true records the earlier document under.
const statBackup = "backup"

// write reads the resource, resolves the content, writes it, records the
// undo when the write created the resource, reads it again, and records the
// backup and the diff, returning the warnings the task should carry. For a
// post, path is the parent: the resource read after is the one its answer's
// Location names.
//
// Whether a write created the resource is the device's own answer, RFC 8040
// section 4.5's for a put (201 Created for a create, 204 No Content for a
// replace) and section 4.4.1's for a post (201 Created, its Location naming
// the new resource), never a read before it that found nothing: a 404 can be
// access control hiding a resource that is there, and an undo deleting it
// would delete what the run did not make. The undo of a create is recorded
// as the 201's status line arrives, before its body is read, so it stands
// whatever happens to the rest of the answer.
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
	var readAt restconf.Path
	created := false
	written, err := client.WriteThen(ctx, req.method, req.datastore, req.path, req.mediaType, body, func(w restconf.Written) error {
		var why string
		readAt, created, why = createdBy(req, had, w)
		if why != "" {
			notes = append(notes, why)
		}
		if !created {
			return nil
		}
		return recordUndo(rc, readAt, req)
	})
	if err != nil {
		var status *restconf.StatusError
		// RFC 8040 section 4.4.1 answers a post of a resource that is there
		// with 409 and resource-denied (rousette does, measured
		// 2026-11-15); section 7 maps data-exists to 409 as well.
		if req.method == restconf.MethodPost && errors.As(err, &status) && status.Status == http.StatusConflict &&
			(status.HasTag("resource-denied") || status.HasTag("data-exists")) {
			if status.Resent > 0 {
				// The first try may have created it before the device
				// refused it; the resource is the device's word, and no undo
				// is recorded, since it may not be this run's.
				return collection.Result{}, notes, &restconf.AnswerLostError{FirstTry: true, Err: fmt.Errorf(
					"the post was refused for too many sessions and sent again (%d times), and the device then answered that the resource it would create is there (%w), which the first try may have created, so no undo is recorded", status.Resent, err)}
			}
			notes = append(notes, "the resource post would create is already there, and post never replaces one, so nothing changed: use put to make it match content")
			return collection.Result{Changed: false}, notes, nil
		}
		if req.method == restconf.MethodDelete && errors.As(err, &status) && status.Status == http.StatusNotFound && status.Resent > 0 {
			// The read before found it; the first try may have removed it.
			return collection.Result{}, notes, firstTry(ctx, rc, client, req, had, maskedBefore, maskErr, fmt.Sprintf(
				"the delete was refused for too many sessions and sent again (%d times), and the device then answered 404 (%v)", status.Resent, err))
		}
		return collection.Result{}, notes, err
	}
	if req.method == restconf.MethodPut && !had && written.Status != http.StatusCreated && written.Resent > 0 {
		// A first try that created the resource leaves the resend a replace.
		return collection.Result{}, notes, firstTry(ctx, rc, client, req, had, maskedBefore, maskErr, fmt.Sprintf(
			"the put of a resource the read before did not find was refused for too many sessions and sent again (%d times), and the device then answered %d, not 201 Created, so no undo is recorded", written.Resent, written.Status))
	}
	if req.method != restconf.MethodPost {
		readAt = req.path
	}
	if readAt == nil {
		return collection.Result{Changed: true}, notes, recordDiff(rc, false, "", nil, true, "", nil)
	}
	after, present, err := client.Read(ctx, req.datastore, readAt, req.mediaType)
	if err != nil {
		return collection.Result{Changed: true}, notes, fmt.Errorf("the write was answered %d, and reading the resource after it failed: %w", written.Status, err)
	}
	maskedAfter, afterErr := recorded(rc, client, after)
	changed := req.method == restconf.MethodPost || created || had != present || differ(before, after, req.asJSON, &notes)
	if created && !present {
		notes = append(notes, "the device answered the write, and the resource it created is not there to read")
	}
	return collection.Result{Changed: changed}, notes, recordDiff(rc, had, maskedBefore, maskErr, present, maskedAfter, afterErr)
}

// firstTry is the error for a write sent again after the device refused it
// for too many sessions whose answer a first try having applied it would also
// explain: IOS XE's refusal suggests a refused write was not applied, which
// was never measured for writes. The resource is read after and the diff
// recorded, so the error says where the resource stands; whether the first
// try or the resend put it there is what is not known (AnswerLostError's
// FirstTry), and no undo is recorded for it.
func firstTry(ctx context.Context, rc sdk.RunbookContext, client *restconf.Client, req writeRequest, had bool, before string, beforeErr error, what string) error {
	after, present, err := client.Read(ctx, req.datastore, req.path, req.mediaType)
	if err != nil {
		return &restconf.AnswerLostError{FirstTry: true, Err: fmt.Errorf("%s, and reading the resource after it failed: %w", what, err)}
	}
	maskedAfter, afterErr := recorded(rc, client, after)
	if err := recordDiff(rc, had, before, beforeErr, present, maskedAfter, afterErr); err != nil {
		return err
	}
	stands := "the read after found the resource there"
	if !present {
		stands = "the read after found the resource not there"
	}
	return &restconf.AnswerLostError{FirstTry: true, Err: fmt.Errorf("%s; %s", what, stands)}
}

// createdBy reads a write's answer: the resource the write created (a put's
// path, or the child of a post's path its Location names) when the device
// answered that it created one, and otherwise why no undo is recorded, when a
// person would expect one. had is whether the read before found the
// resource.
func createdBy(req writeRequest, had bool, w restconf.Written) (restconf.Path, bool, string) {
	switch req.method {
	case restconf.MethodPut:
		switch {
		case w.Status == http.StatusCreated && !had:
			return req.path, true, ""
		case w.Status == http.StatusCreated:
			return nil, false, "the device answered 201 Created to a put of a resource the read before found there, so no undo is recorded: deleting it could remove what this run did not make"
		case !had:
			return nil, false, fmt.Sprintf("the device answered %d, not 201 Created, to a put of a resource the read before did not find, so it is read as a replace (RFC 8040 section 4.5 answers a create with 201) and no undo is recorded", w.Status)
		}
	case restconf.MethodPost:
		switch {
		case w.Location == nil:
			return nil, false, "the device named no Location this client could read for the resource post created, so no undo is recorded and nothing was read back"
		case !w.Location.Child(req.path):
			return nil, false, "the device's Location for the resource post created does not name one node directly under path, so no undo is recorded and nothing was read back: deleting what it names could remove what this run did not make"
		case w.Status != http.StatusCreated:
			return nil, false, fmt.Sprintf("the device answered the post %d, not 201 Created, so no undo is recorded and nothing was read back", w.Status)
		}
		return w.Location, true, ""
	}
	return nil, false, ""
}

// differ reports whether two reads of a resource differ in their canonical
// form; when either cannot be read as one, it compares the bytes and says
// so.
func differ(before, after []byte, asJSON bool, notes *[]string) bool {
	a, errA := yangdoc.Canonical(before, asJSON)
	b, errB := yangdoc.Canonical(after, asJSON)
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
	byValue, err := client.Mask(string(body))
	if err != nil {
		return "", err
	}
	masked, found, err := netcfg.MaskOutput(byValue)
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
