// This file is the Sync button and the Playbooks tab: the two halves of
// "the repository is here now".
//
// # No credential passes through this file
//
// A private clone authenticates as an ordinary Credential, and resolving
// one means reading a decrypted secret. This view cannot: internal/archtest
// fails the build if the UI side ever reaches the package that decrypts,
// and that rule is why syncAction hands the syncer nothing.
//
// The syncer resolves the project's own credential id internally, through
// an interface internal/project declares and the composition root
// implements. So the secret exists inside one clone and is never held by a
// page, a handler or a form.
package projects

import (
	"context"
	"errors"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// sectionLimit bounds one section's read. A section is a drill-down, not a
// listing.
const sectionLimit = 200

// syncEnqueuer is what the Sync and Cancel controls need from the project
// runner: start a clone in the background, and stop one already running. It
// is *project.Runner in a real controller; the actions hold the interface so
// a test can drive the buttons without a real clone.
type syncEnqueuer interface {
	// Enqueue starts a clone and returns the id of the attempt it started,
	// which this view has no use for: the page it lands on reads the badge.
	// The actor is who asked, taken from the request's identity.
	Enqueue(ctx context.Context, id int, actor string) (int, error)

	// Cancel stops this project's running clone, reporting whether there
	// was one to stop.
	Cancel(projectID int) bool
}

// cancelAction stops a clone that is taking too long.
//
// It prompts for nothing for the same reason Sync does not: everything it
// needs is on the record. Whether a clone was actually running is not
// reported back as an error, because the page it lands on already answers
// that: a sync that finished between the button being drawn and pressed is
// not a mistake the person made.
func cancelAction(runner syncEnqueuer) view.RecordAction {
	return view.RecordAction{
		Name:     "cancel-sync",
		Label:    "Cancel sync",
		Heading:  "Stop this project's running sync",
		Endpoint: &apispec.CancelProjectSync,
		Submit: func(_ context.Context, id string, _ view.Values) (string, view.FieldErrors, error) {
			errs := view.FieldErrors{}
			numeric, err := strconv.Atoi(id)
			if err != nil {
				return "", errs, project.ErrNotFound
			}
			runner.Cancel(numeric)
			return "", errs, nil
		},
	}
}

// syncAction starts an asynchronous clone of the project's working tree.
//
// It prompts for nothing, which is why it has no Fields: everything a sync
// needs is already on the record. The confirmation a person gets is the
// heading, and the result is the badge on the page they land back on, which
// the Refresh spec keeps current as the background clone runs.
func syncAction(enqueue syncEnqueuer) view.RecordAction {
	return view.RecordAction{
		Name:     "sync",
		Label:    "Sync",
		Heading:  "Fetch this project's source",
		Endpoint: &apispec.SyncProject,
		Submit: func(ctx context.Context, id string, _ view.Values) (string, view.FieldErrors, error) {
			errs := view.FieldErrors{}
			numeric, err := strconv.Atoi(id)
			if err != nil {
				return "", errs, project.ErrNotFound
			}

			// The actor comes from the request's identity, never from the
			// submission: a caller who could name it could forge the audit
			// trail it exists to be. This is the same rule the template
			// launch action follows.
			identity, ok := api.IdentityFromContext(ctx)
			if !ok || identity == nil || identity.Subject == "" {
				return "", errs, errors.New("no identity on the request context")
			}

			switch _, err := enqueue.Enqueue(ctx, numeric, identity.Subject); {
			case err == nil, errors.Is(err, project.ErrSyncInProgress):
				// Started, or one is already running: either way the page
				// this lands on shows a running badge, which is the answer
				// a second press would ask for.
				return "", errs, nil
			case errors.Is(err, project.ErrNotSyncable):
				// A project with nothing to fetch is a configuration
				// problem, so it is said against the control that causes it
				// rather than filed as a failed sync in the background.
				errs.Add("scm_url", err.Error())
				return "", errs, nil
			default:
				return "", errs, err
			}
		},
	}
}

// playbooksSection is what a sync produced: the files in the working tree a
// Template can name.
//
// Empty is the interesting state here and it has two causes worth telling
// apart, so the text names both. A project that has never synced has no
// tree to read; a project that synced fine but holds nothing runnable is a
// different problem with a different fix.
func playbooksSection(syncer project.Syncer, store project.Store) view.Section {
	return view.Section{
		Status:  view.StatusImplemented,
		Title:   "Playbooks",
		Summary: "The runnable files in this project's working tree. A template names one of these.",
		Empty: "Nothing runnable is checked out. Either this project has not synced yet, " +
			"or its tree holds no .yml or .yaml file in the root or a playbooks/ directory.",
		Fields: []view.Field{
			{Name: "path", Label: "PATH", Kind: view.KindText, InList: true, MobilePrimary: true},
		},
		Rows: func(ctx context.Context, parentID string) ([]view.Row, error) {
			if parentID == "" {
				return nil, nil
			}
			numeric, err := strconv.Atoi(parentID)
			if err != nil {
				return nil, nil
			}
			p, err := store.Get(ctx, numeric)
			if err != nil {
				return nil, err
			}

			found, err := syncer.Playbooks(ctx, p)
			if err != nil {
				return nil, err
			}
			if len(found) > sectionLimit {
				found = found[:sectionLimit]
			}

			rows := make([]view.Row, 0, len(found))
			for _, path := range found {
				rows = append(rows, view.Row{
					ID:    path,
					Cells: view.Cells{"path": path},
				})
			}
			return rows, nil
		},
	}
}
