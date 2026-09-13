// Package project is source control: the repositories this platform clones
// and the playbooks inside them a Template can run.
//
// It is the answer to "where does automation come from". Before it, a
// Template could only name something compiled into the binary through
// launch.Catalog, so everything runnable had to be written by somebody with
// commit access to this module. A Project makes work that lives in
// somebody else's repository runnable here.
//
// # The port, and what is deliberately not in it
//
// Store is persistence and Syncer is the side of it that touches a network
// and a disk. They are separate interfaces because they fail in unrelated
// ways and are tested against unrelated things: a store test wants a
// database, a syncer test wants a repository. A single interface covering
// both would force every fake to implement the half it does not care about.
//
// There is no secret material here. A private repository authenticates as
// an ordinary Credential of the scm kind, resolved through credstore, so
// this package never stores, encrypts or redacts one. What it does have to
// do is keep a credential out of the places an error message goes, which
// is what scrubURL exists for.
package project

import (
	"context"
	"errors"
	"time"
)

// Common failures a caller distinguishes.
var (
	// ErrNotFound is an unknown project.
	ErrNotFound = errors.New("project: not found")

	// ErrExists is a name already taken within the organization.
	ErrExists = errors.New("project: a project with this name already exists in this organization")

	// ErrNotSyncable is a project whose source cannot be fetched: an
	// unsupported scm type, or a git project with no URL.
	ErrNotSyncable = errors.New("project: this project has no fetchable source")
)

// SCMType is how a project's source is reached.
type SCMType string

// The source control types the schema records. Only SCMGit is implemented;
// the other two exist because AWX has them and an import needs somewhere
// truthful to put one rather than silently calling it git.
const (
	SCMGit     SCMType = "git"
	SCMArchive SCMType = "archive"
	SCMManual  SCMType = "manual"
)

// SyncStatus is where a project's last sync got to.
type SyncStatus string

// The sync states. "never" is distinct from "failed" on purpose: a project
// that has not been synced yet and one whose sync failed look identical in
// a listing if both read as empty, and they call for opposite actions.
const (
	SyncNever     SyncStatus = "never"
	SyncPending   SyncStatus = "pending"
	SyncRunning   SyncStatus = "running"
	SyncSucceeded SyncStatus = "succeeded"
	SyncFailed    SyncStatus = "failed"
)

// Project is one source repository.
type Project struct {
	ID          int
	Name        string
	Description string

	SCMType   SCMType
	SCMURL    string
	SCMBranch string

	// CredentialID authenticates the clone, zero when the repository is
	// public.
	CredentialID int

	OrganizationID   int
	OrganizationName string

	// LocalPath is where the working tree lives, keyed by id so a rename
	// does not orphan a checkout. Empty until the first sync.
	LocalPath string

	// Revision is the commit the working tree is at, as a full hash.
	Revision string

	SyncStatus   SyncStatus
	SyncError    string
	LastSyncedAt *time.Time
}

// Syncable reports whether this project has a source that can be fetched.
func (p Project) Syncable() bool {
	return p.SCMType == SCMGit && p.SCMURL != ""
}

// ShortRevision is the revision abbreviated for display, empty when there
// is none. Seven characters, which is git's own default abbreviation.
func (p Project) ShortRevision() string {
	if len(p.Revision) < 7 {
		return p.Revision
	}
	return p.Revision[:7]
}

// Query bounds a listing.
type Query struct {
	OrganizationID int
	Limit          int
}

// Store is persistence for projects.
type Store interface {
	Create(ctx context.Context, p Project) (Project, error)
	Get(ctx context.Context, id int) (Project, error)
	List(ctx context.Context, q Query) ([]Project, error)
	Update(ctx context.Context, p Project) error
	Delete(ctx context.Context, id int) error

	// RecordSync saves the outcome of one sync attempt. It is separate from
	// Update because the two write disjoint halves of the row and race
	// otherwise: a sync finishing while somebody is renaming the project
	// must not put the old name back, and an Update must not reset a
	// revision it knows nothing about.
	RecordSync(ctx context.Context, id int, result Result) error
}

// Result is the outcome of one sync attempt.
type Result struct {
	Status    SyncStatus
	Revision  string
	LocalPath string

	// Err is why a failed sync failed, already scrubbed of any credential
	// material. It is a string rather than an error because it is stored
	// and rendered, never unwrapped.
	Err string

	At time.Time
}

// Syncer fetches a project's source onto local disk.
type Syncer interface {
	// Sync clones or fast-forwards the project's working tree and reports
	// what it ended up at. A returned Result with SyncFailed carries the
	// reason in Err; the error return is reserved for a failure to even
	// attempt, such as an unsyncable project.
	Sync(ctx context.Context, p Project, auth Auth) (Result, error)

	// Playbooks lists the runnable files in a synced working tree, as
	// paths relative to its root.
	Playbooks(ctx context.Context, p Project) ([]string, error)
}

// Auth is how a clone authenticates, resolved from a Credential by the
// caller so this package never reads the credential store itself.
//
// An empty Auth is a public repository. Both fields are secret-adjacent and
// neither is ever logged or stored: see scrubURL for the one place they
// could otherwise escape.
type Auth struct {
	Username string

	// Password is a password or a personal access token. Over HTTPS a
	// token is what a forge actually issues, and git treats the two
	// identically, so there is one field rather than two that would have
	// to be kept in step.
	Password string
}

// Empty reports whether this is an unauthenticated clone.
func (a Auth) Empty() bool { return a.Username == "" && a.Password == "" }
