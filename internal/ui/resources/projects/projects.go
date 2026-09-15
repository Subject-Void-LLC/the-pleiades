// Package projects is the Projects view: the source repositories this
// deployment runs automation out of.
//
// It was a declared shape with no backing port, because nothing in this
// module could clone a repository. internal/project can now, so this is
// real: create a project, press Sync, and the playbooks in it become
// things a Template can run.
//
// # Sync is asynchronous
//
// Pressing Sync no longer blocks the page on the clone. It starts the clone
// on internal/project's Runner and returns at once; the badge moves to
// running, and the Refresh spec keeps it current until it settles on
// succeeded or failed. A repository large enough to take a while no longer
// holds the page open while it fetches.
//
// The parts of AWX's "a project update is a Job" that mattered are here
// without the project having to become one: the fetch runs in the
// background, its output streams live to a Sync output page, and every
// completed attempt is kept as a Sync history tab. What a job's machinery
// would have added beyond that is a relaunch, which for a sync is just
// pressing Sync again.
//
// It is reachable only because internal/ui/resources/registrars.go names it
// (FAILURE_PATTERNS.md #52).
package projects

import (
	"context"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "projects"

// declaredFields are the shape.
func declaredFields(orgs inventory.OrganizationLister, creds credentialLister) []view.Field {
	return []view.Field{
		{
			Name: "name", Label: "NAME", Kind: view.KindText,
			Required: true, MaxLen: 253, InList: true, InForm: true, MobilePrimary: true,
			Autocomplete: "off",
			Help:         "What this repository is called here, within its organization.",
		},
		{
			Name: "description", Label: "DESCRIPTION", Kind: view.KindLongText,
			MaxLen: 1024, InForm: true,
			Help: "What is in it, for somebody who did not add it.",
		},
		{
			Name: "organization", Label: "ORGANIZATION", Kind: view.KindSelect,
			Required: true, Immutable: true, InList: true, InForm: true,
			References: "organizations",
			Help:       "The tenant that owns this project. Fixed once saved.",
			Options: func(ctx context.Context) ([]view.Option, error) {
				found, err := orgs.ListOrganizations(ctx)
				if err != nil {
					return nil, err
				}
				out := make([]view.Option, 0, len(found))
				for _, org := range found {
					out = append(out, view.Option{Label: org.Name, Value: strconv.Itoa(org.ID)})
				}
				return out, nil
			},
		},
		{
			Name: "scm_type", Label: "SOURCE", Kind: view.KindSelect,
			Required: true, InList: true, InForm: true,
			Help: "How the content is reached. Only git is implemented; the other two exist so an AWX import has somewhere truthful to record what it was.",
			Options: func(context.Context) ([]view.Option, error) {
				return []view.Option{
					{Label: "Git", Value: string(project.SCMGit)},
					{Label: "Archive (not implemented)", Value: string(project.SCMArchive)},
					{Label: "Manual (not implemented)", Value: string(project.SCMManual)},
				}, nil
			},
		},
		{
			Name: "scm_url", Label: "URL", Kind: view.KindText,
			MaxLen: 2048, InList: true, InForm: true, Autocomplete: "off",
			Help: "The repository to clone. Prefer an https address with a credential over embedding a token in the URL: a URL's userinfo ends up in error messages.",
		},
		{
			Name: "credential", Label: "CREDENTIAL", Kind: view.KindSelect,
			InForm: true, References: "credentials",
			Help: "How a private repository is authenticated: a token, a username and password, or an SSH key " +
				"with its passphrase. Leave it unset for a public repository. Only Source Control credentials " +
				"are offered, so a credential missing from this list is either the wrong type or carries no secret.",
			Options: credentialOptions(creds),
		},
		{
			Name: "scm_branch", Label: "BRANCH", Kind: view.KindText,
			MaxLen: 255, InForm: true, Autocomplete: "off",
			Help: "The branch, tag or commit to check out. Leave empty for whatever the remote's own default branch is.",
		},
		{
			Name: "sync_status", Label: "SYNC", Kind: view.KindBadge, InList: true,
			Help:       "Where the last sync got to. A project that has never synced has no playbooks to offer a template.",
			BadgeClass: syncBadge,
		},
		{
			Name: "revision", Label: "REVISION", Kind: view.KindReadOnly, InList: true,
			Help: "The commit the working tree is at, abbreviated. Empty until a sync has succeeded once.",
		},
		{
			Name: "sync_error", Label: "LAST ERROR", Kind: view.KindReadOnly,
			Help: "Why the last sync failed. Credential material is stripped before this is stored.",
		},
		{
			Name: "last_synced", Label: "LAST SYNCED", Kind: view.KindTimestamp, InList: true,
		},
	}
}

// syncBadge colours a sync state. Failure and never-synced are deliberately
// different: they look the same in a listing if both read as empty, and
// they call for opposite actions.
func syncBadge(status string) string {
	switch project.SyncStatus(status) {
	case project.SyncSucceeded:
		return "badge-ok"
	case project.SyncFailed:
		return "badge-failed"
	case project.SyncRunning, project.SyncPending:
		return "badge-changed"
	default:
		return "badge-neutral"
	}
}

// reader adapts the read half.
type reader struct{ store project.Store }

func (r reader) List(ctx context.Context, q view.Query) (view.Page[project.Project], error) {
	found, err := r.store.List(ctx, project.Query{Limit: q.Limit})
	if err != nil {
		return view.Page[project.Project]{}, err
	}
	if search := strings.ToLower(strings.TrimSpace(q.Search)); search != "" {
		kept := found[:0]
		for _, p := range found {
			if strings.Contains(strings.ToLower(p.Name), search) || strings.Contains(strings.ToLower(p.SCMURL), search) {
				kept = append(kept, p)
			}
		}
		found = kept
	}
	return view.Page[project.Project]{Items: found}, nil
}

func (r reader) Get(ctx context.Context, id string) (project.Project, error) {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return project.Project{}, project.ErrNotFound
	}
	return r.store.Get(ctx, numeric)
}

// writer adapts the write half.
//
// It holds the credential lister as well as the store, because the one rule
// this view enforces beyond the store's own is about a credential: a
// project may only name one that can actually authenticate a clone. Bind
// cannot check it, having no context to read with, so it lands here, on the
// path every write takes rather than only the one the form takes.
type writer struct {
	store project.Store
	creds credentialLister
}

func (w writer) Create(ctx context.Context, p project.Project) (string, error) {
	if err := usableForGit(ctx, w.creds, p.CredentialID); err != nil {
		return "", err
	}
	created, err := w.store.Create(ctx, p)
	if err != nil {
		return "", asFault(err)
	}
	return strconv.Itoa(created.ID), nil
}

func (w writer) Update(ctx context.Context, id string, p project.Project) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return project.ErrNotFound
	}
	existing, err := w.store.Get(ctx, numeric)
	if err != nil {
		return err
	}
	p.ID = numeric
	// Immutable, so an edit never carries it and the store must not be
	// told to move the project between tenants.
	p.OrganizationID = existing.OrganizationID
	if err := usableForGit(ctx, w.creds, p.CredentialID); err != nil {
		return err
	}
	return asFault(w.store.Update(ctx, p))
}

func (w writer) Delete(ctx context.Context, id string) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return project.ErrNotFound
	}
	return w.store.Delete(ctx, numeric)
}

// asFault blames a name collision on the control that caused it, rather
// than answering a duplicate name with a 500.
func asFault(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), project.ErrExists.Error()) {
		return view.FieldFault{Field: "name", Message: "A project with this name already exists in this organization."}
	}
	return err
}

// Register wires this view over the live project store. The runner starts a
// Sync's clone in the background; the syncer is still needed for the
// read-only Playbooks tab, which reads a synced tree rather than fetching.
func Register(store project.Store, syncer project.Syncer, runner syncEnqueuer, orgs inventory.OrganizationLister, creds credentialLister) error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Projects",
		NavLabel: "PROJECTS",
		NavOrder: 55,
		NavGroup: view.NavGroupResources,
		Summary:  "Where automation content comes from: a synced repository of playbooks.",
		Status:   view.StatusImplemented,
		IDField:  "name",
		Fields:   declaredFields(orgs, creds),
		Actions:  []view.RecordAction{syncAction(runner), cancelAction(runner)},
		// Cancel is offered only while a clone is actually in flight. A
		// button that could only report "nothing was running" is the
		// affordance this project withholds rather than draws.
		Applies: func(row view.Row, rel auth.LinkRel) bool {
			if rel == apispec.CancelProjectSync.Rel {
				return project.SyncStatus(row.Cells["sync_status"]) == project.SyncRunning
			}
			return true
		},
		// Watching a fetch is what a large repository needs: a badge says
		// how it ended, and this says what it is doing meanwhile. Built from
		// the API's own prefix and the endpoint's own pattern, so a change
		// to either moves this with it rather than leaving a hardcoded path
		// that still parses and no longer resolves.
		Stream: &view.StreamSpec{
			Title:       "Sync output",
			PathPattern: api.APIVersionPrefix + apispec.StreamProjectSyncLogs.Pattern,
		},
		Sections: []view.Section{playbooksSection(syncer, store), historySection(store)},
		Ops: view.Ops{
			List:   &apispec.ListProjects,
			Get:    &apispec.GetProject,
			Create: &apispec.CreateProject,
			Update: &apispec.UpdateProject,
			Delete: &apispec.DeleteProject,
		},
		Handlers: view.MustBind[project.Project](reader{store}, writer{store, creds}, view.Projector[project.Project]{
			Row: func(p project.Project) view.Row {
				return view.Row{
					ID:   strconv.Itoa(p.ID),
					Refs: map[string]string{"organization": strconv.Itoa(p.OrganizationID)},
					Cells: view.Cells{
						"name":         p.Name,
						"description":  p.Description,
						"organization": p.OrganizationName,
						"scm_type":     string(p.SCMType),
						"scm_url":      p.SCMURL,
						"scm_branch":   p.SCMBranch,
						"sync_status":  string(p.SyncStatus),
						"revision":     p.ShortRevision(),
						"sync_error":   p.SyncError,
						"last_synced":  timestamp(p),
					},
				}
			},
			Form: func(p project.Project) map[string]string {
				return map[string]string{
					"name":         p.Name,
					"description":  p.Description,
					"organization": strconv.Itoa(p.OrganizationID),
					"scm_type":     string(p.SCMType),
					"scm_url":      p.SCMURL,
					"scm_branch":   p.SCMBranch,
					"credential":   credentialValue(p),
				}
			},
			Bind: func(v view.Values) (project.Project, view.FieldErrors) {
				errs := view.FieldErrors{}
				p := project.Project{
					Name:        strings.TrimSpace(v.Get("name")),
					Description: strings.TrimSpace(v.Get("description")),
					SCMType:     project.SCMType(v.Get("scm_type")),
					SCMURL:      strings.TrimSpace(v.Get("scm_url")),
					SCMBranch:   strings.TrimSpace(v.Get("scm_branch")),
					// Zero means no credential, which is a public
					// repository rather than an error: the chooser's empty
					// option is a real choice.
					CredentialID: optionalID(v.Get("credential")),
				}
				if !v.Editing() {
					org, err := strconv.Atoi(strings.TrimSpace(v.Get("organization")))
					if err != nil || org < 1 {
						errs.Add("organization", "Choose the organization this project belongs to.")
					}
					p.OrganizationID = org
				}
				// Caught here rather than at sync time, because a git
				// project with no URL is a project that can never do the one
				// thing it exists for, and finding that out by pressing Sync
				// and reading a failure is a worse way to be told.
				if p.SCMType == project.SCMGit && p.SCMURL == "" {
					errs.Add("scm_url", "A git project needs a repository URL.")
				}
				return p, errs
			},
		}),
	})
}

// timestamp renders when the last sync ran, empty when none has.
func timestamp(p project.Project) string {
	if p.LastSyncedAt == nil {
		return ""
	}
	return p.LastSyncedAt.UTC().Format("2006-01-02T15:04:05Z")
}
