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
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// sectionLimit bounds one section's read. A section is a drill-down, not a
// listing.
const sectionLimit = 200

// syncAction clones or fast-forwards the project's working tree.
//
// It prompts for nothing, which is why it has no Fields: everything a sync
// needs is already on the record. The confirmation a person gets is the
// heading, and the result is the badge on the page they land back on.
func syncAction(store project.Store, syncer project.Syncer) view.RecordAction {
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
			p, err := store.Get(ctx, numeric)
			if err != nil {
				return "", errs, err
			}

			result, err := syncer.Sync(ctx, p)
			if err != nil {
				// A project with nothing to fetch is a configuration
				// problem rather than a failure to record: saying so
				// against the control that causes it is more useful than
				// filing it as a failed sync.
				errs.Add("scm_url", err.Error())
				return "", errs, nil
			}
			if err := store.RecordSync(ctx, numeric, result); err != nil {
				return "", errs, err
			}
			return "", errs, nil
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
