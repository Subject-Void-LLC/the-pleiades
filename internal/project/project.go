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

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
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

	// ErrSyncInProgress is a claim on a project whose sync is already
	// running. It is what makes pressing Sync twice safe: the second press
	// is refused rather than starting a second clone into the same working
	// tree, which race each other on the one path a project keys its
	// checkout by.
	ErrSyncInProgress = errors.New("project: a sync is already running for this project")
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

	// BeginSync claims a project for an asynchronous sync, moving it to
	// running and returning the record to hand to the syncer. The move is a
	// compare-and-swap that matches only a project not already running, so
	// two presses of Sync cannot both start a clone into the one working
	// tree a project keys by id; the loser is told ErrSyncInProgress. An
	// unsyncable project is refused synchronously with ErrNotSyncable
	// rather than claimed and failed where nobody is looking.
	BeginSync(ctx context.Context, id int) (Project, error)

	// ResetInterruptedSyncs moves every running project to failed and
	// reports how many it moved. A sync runs in memory, so a running row at
	// process start is a clone whose process is gone; left as it is, its
	// project could never be synced again, because BeginSync's swap would
	// never match. It is meant to run once at startup.
	ResetInterruptedSyncs(ctx context.Context) (int, error)

	// ListSyncRuns returns a project's completed sync attempts, newest
	// first, capped at limit. It is the history behind the latest outcome
	// the project row itself carries.
	ListSyncRuns(ctx context.Context, projectID, limit int) ([]SyncRun, error)
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

	// StartedAt is when the attempt began. The Runner stamps it rather than
	// the syncer, because the syncer reports what it found and the runner is
	// what knows when it was asked. It is here rather than on a separate
	// argument so recording an outcome stays one call: a history row needs
	// both ends of the attempt, and a duration cannot answer "what was the
	// last thing that ran, and when".
	//
	// Zero means unrecorded, which is what a caller that bypasses the runner
	// produces; RecordSync falls back to At so a history row is never
	// written with a start in the distant past.
	StartedAt time.Time
}

// SyncRun is one completed attempt to fetch a project's source.
//
// Only terminal attempts are recorded, so Status is SyncSucceeded or
// SyncFailed. An attempt still running is visible on the project's own
// SyncStatus; see the SyncRun ent schema for why it is not also a row here.
type SyncRun struct {
	ID       int
	Status   SyncStatus
	Revision string

	// Err is why this attempt failed, scrubbed the same way Result.Err is.
	Err string

	StartedAt  time.Time
	FinishedAt time.Time
}

// Took reports how long this attempt ran.
func (r SyncRun) Took() time.Duration { return r.FinishedAt.Sub(r.StartedAt) }

// Syncer fetches a project's source onto local disk.
type Syncer interface {
	// Sync clones or fast-forwards the project's working tree and reports
	// what it ended up at. A returned Result with SyncFailed carries the
	// reason in Err; the error return is reserved for a failure to even
	// attempt, such as an unsyncable project.
	//
	// It takes no Auth. The credential is resolved inside the
	// implementation, from the id the project already carries, so a caller
	// asking for a sync never holds a secret and cannot leak one it was
	// handed.
	Sync(ctx context.Context, p Project) (Result, error)

	// Playbooks lists the runnable files in a synced working tree, as
	// paths relative to its root.
	Playbooks(ctx context.Context, p Project) ([]string, error)
}

// Auth is how a clone authenticates.
//
// An empty Auth is a public repository. Every field here is secret material
// or adjacent to it, and none of it is ever logged, stored or rendered: a
// value lives in this struct for the length of one clone. scrubURL is the
// one place it could otherwise escape, because transport errors quote the
// URL they tried.
//
// The three shapes a forge actually offers, in one type because git treats
// the first two identically and the transport decides which applies:
//
//   - a token alone, which is what GitHub, GitLab and Bitbucket issue in
//     place of a password. Username is then whatever the forge wants as a
//     placeholder and is supplied by authMethod, not by the operator.
//   - a username and password over HTTPS.
//   - an SSH private key, optionally encrypted, in which case Passphrase
//     unlocks it. A key with a passphrase and no passphrase supplied fails
//     at parse time with a clear error rather than hanging on a prompt,
//     which is the difference between an unattended sync and an
//     interactive one.
type Auth struct {
	Username string

	// Password is a password or a personal access token. One field rather
	// than two, because git sends both the same way and keeping two in
	// step would be two chances to send the wrong one.
	Password string

	// PrivateKey is a PEM-encoded SSH private key.
	PrivateKey []byte

	// Passphrase decrypts PrivateKey, empty for an unencrypted key. It is
	// never a password for anything else: an encrypted key with the wrong
	// passphrase must fail as a key problem rather than fall back to a
	// password attempt that would put this value on the wire.
	Passphrase string
}

// Empty reports whether this is an unauthenticated clone.
func (a Auth) Empty() bool {
	return a.Username == "" && a.Password == "" && len(a.PrivateKey) == 0
}

// UsesKey reports whether this authenticates with an SSH key.
func (a Auth) UsesKey() bool { return len(a.PrivateKey) > 0 }

// The credential inputs a git clone can use, named once here because three
// places have to agree about them: the chooser that offers a credential,
// the check that refuses an unusable one, and the composition root that
// maps a resolved credential onto an Auth. They were duplicated as string
// literals in the last of those, with nothing keeping them in step.
//
// These are AWX's own names, which is what internal/credtype/managed's
// Source Control and Machine types declare and what an AWX export already
// carries.
const (
	InputUsername   = "username"
	InputPassword   = "password"
	InputPrivateKey = "ssh_key_data"
	InputPassphrase = "ssh_key_unlock"
)

// AuthenticatesGit reports whether a credential may authenticate a clone.
//
// Two conditions, and the first is the one that matters. The credential's
// KIND must be scm: a project sync must not be reachable with a credential
// issued for something else, whatever that credential happens to carry. A
// Machine credential opens shells on managed devices and a cloud credential
// spends money, and neither becomes a source-control credential by
// declaring a field with a familiar name.
//
// This replaced a purely structural test that asked only what a credential
// carried. That was too loose in exactly one way, and it was not
// hypothetical: any custom type declaring an input called "password" passed
// it, whatever the type was for.
//
// The second condition is that it actually supplies something usable, which
// catches an scm credential holding only a username. Git will attempt a
// clone with that, fail on the far side, and report something about
// authentication that never says the credential carried no secret.
//
// It decides this from a redacted projection, without a plaintext read. A
// stored secret reads back as a marker rather than a dropped key, and an
// externally sourced one is named in external instead: both mean the input
// is supplied, and consulting only the first would refuse every credential
// whose password lives in a secret manager.
func AuthenticatesGit(kind credtype.Kind, inputs, external map[string]string) bool {
	if kind != credtype.KindSCM {
		return false
	}
	return supplies(inputs, external, InputPassword) || supplies(inputs, external, InputPrivateKey)
}

// supplies reports whether an input has a value from either source.
func supplies(inputs, external map[string]string, id string) bool {
	return inputs[id] != "" || external[id] != ""
}

// AuthResolver turns a credential id into the values a clone needs.
//
// It is an interface declared here and implemented by the composition root,
// rather than this package importing the credential resolver directly, and
// that is load bearing rather than stylistic. internal/archtest asserts
// that internal/api never DEPENDS on the package which produces plaintext
// credential values, transitively rather than by direct import. The API's
// project handler holds a Syncer, so a Syncer that imported the resolver
// would put the resolver in the API's dependency graph and fail that
// assertion, correctly: it exists so "no plaintext read API" is a property
// of the build rather than a promise.
//
// A nil resolver means every clone is unauthenticated, which is the honest
// behaviour for a deployment that has wired no credentials.
type AuthResolver interface {
	// ResolveAuth returns the credential's values, or an empty Auth when
	// id is zero.
	ResolveAuth(ctx context.Context, credentialID int) (Auth, error)
}
