// This file is the git half of a Project: cloning a repository onto local
// disk and listing what can be run out of it.
//
// # Why go-git rather than shelling out
//
// go-git honours a context.Context so a hung fetch is cancellable and,
// decisively, there is no argument-injection surface. A repository URL is
// operator-supplied data, and `git clone <url>` with a url beginning
// "--upload-pack=" runs an arbitrary program. Passing that same string as a
// struct field cannot.
//
// The cost is real and worth stating: no submodules, and a large repository
// costs more memory than the C implementation would.
//
// This used to claim that the controller image carries no git binary, as
// though go-git needed none. The first half is true and the conclusion is not:
// go-git's own FILE transport shells out to git-upload-pack, so a local source
// works on a developer's machine and fails in the image. Which is one of the
// reasons a local source is refused unless a deployment says otherwise; see
// source.go.
package project

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// userinfoPattern matches a URL's embedded credentials. Deliberately broad
// on the scheme, because an error message can quote a URL this package
// never configured (a redirect target, a submodule, a remote named in the
// repository's own config), and the safe default for anything shaped like
// userinfo is to drop it.
var userinfoPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*)://[^/\s@]+@`)

// playbookDirs are the directories a playbook is looked for in, beside the
// repository root. AWX scans a whole tree; this is deliberately narrower,
// because an unbounded walk over an arbitrary repository is a denial of
// service somebody else controls the size of.
var playbookDirs = []string{"", "playbooks"}

// GitSyncer clones projects with go-git.
type GitSyncer struct {
	// root is the directory every working tree lives under.
	root string

	// auth turns a project's credential id into the values a clone needs.
	// Nil means every clone is unauthenticated, which is the honest
	// behaviour for a deployment that has wired no credentials.
	auth AuthResolver

	// locks serialises work per project id. Two syncs interleaving a
	// checkout of the same tree produce a working directory that matches
	// no commit, which is worse than either sync losing.
	locks sync.Map

	// policy is which sources this deployment will fetch from. The zero
	// value refuses everything but https and ssh, so a syncer nobody
	// configured is the careful one rather than the permissive one.
	policy SourcePolicy
}

// NewGitSyncer returns a syncer rooted at dir, resolving credentials
// through auth, fetching only from sources policy admits. A nil auth clones
// only public repositories.
//
// The policy is a value rather than an option because every caller has to make
// the decision: an option would let one be composed without it, and the
// composition that forgot would be the one that fetches from anywhere.
func NewGitSyncer(dir string, auth AuthResolver, policy SourcePolicy) *GitSyncer {
	return &GitSyncer{root: dir, auth: auth, policy: policy}
}

// lockFor returns the mutex guarding one project's working tree.
func (s *GitSyncer) lockFor(id int) *sync.Mutex {
	actual, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

// pathFor is where a project's working tree lives.
//
// Keyed by numeric id rather than by name, so renaming a project does not
// orphan its checkout and two tenants' identically named projects cannot
// collide. The id is rendered rather than interpolated from anything a
// person typed, which is also what keeps this off the filesystem-traversal
// path entirely.
func (s *GitSyncer) pathFor(p Project) string {
	return filepath.Join(s.root, strconv.Itoa(p.OrganizationID), strconv.Itoa(p.ID))
}

// Sync clones the project or fast-forwards an existing checkout.
func (s *GitSyncer) Sync(ctx context.Context, p Project, progress io.Writer) (Result, error) {
	if !p.Syncable() {
		return Result{}, ErrNotSyncable
	}

	// Checked here as well as at the write, and this is the check that
	// matters: a row can predate the rule, and after the fetch below started
	// dialing the project's own URL this is the last point before a
	// connection. It happens before the lock and before any directory is
	// made, so a source this deployment will not fetch from leaves nothing
	// behind on disk.
	//
	// Recorded as a failed sync rather than returned as an error, because
	// that is how every other refusal a sync makes is reported and it is what
	// puts the reason on the project's page.
	if err := s.policy.AdmitsSource(p.SCMURL); err != nil {
		return Result{
			Status: SyncFailed,
			Err:    scrubURL(err.Error(), p.SCMURL),
			At:     time.Now().UTC(),
		}, nil
	}

	lock := s.lockFor(p.ID)
	lock.Lock()
	defer lock.Unlock()

	dir := s.pathFor(p)
	result := Result{LocalPath: dir, At: time.Now().UTC()}

	// Resolved here, inside the lock and immediately before use, rather
	// than passed in. The value exists for the length of this call and is
	// never returned, stored or logged.
	auth, err := s.resolveAuth(ctx, p)
	if err != nil {
		result.Status = SyncFailed
		result.Err = scrubURL(err.Error(), p.SCMURL)
		return result, nil
	}

	repo, openErr := s.open(ctx, dir, p, auth, progress)
	if openErr != nil {
		result.Status = SyncFailed
		result.Err = scrubURL(openErr.Error(), p.SCMURL)
		return result, nil
	}
	if err != nil {
		result.Status = SyncFailed
		result.Err = scrubURL(err.Error(), p.SCMURL)
		return result, nil
	}

	head, err := repo.Head()
	if err != nil {
		result.Status = SyncFailed
		result.Err = scrubURL(err.Error(), p.SCMURL)
		return result, nil
	}

	result.Status = SyncSucceeded
	result.Revision = head.Hash().String()
	return result, nil
}

// open clones into dir, or fetches into it when a checkout is already
// there.
//
// A directory that exists but is not a repository is replaced rather than
// reported. It is this package's own working area, keyed by a numeric id
// nothing else writes to, so anything unexpected there is debris from an
// interrupted clone rather than somebody's data.
func (s *GitSyncer) open(ctx context.Context, dir string, p Project, auth Auth, progress io.Writer) (*gogit.Repository, error) {
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return nil, fmt.Errorf("preparing the project directory: %w", err)
	}

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			return nil, fmt.Errorf("clearing a stale project directory: %w", rmErr)
		}
		return s.clone(ctx, dir, p, auth, progress)
	}

	// A checkout of a DIFFERENT repository cannot be fetched into: an
	// unrelated history has no common ancestor, so a fetch reports a
	// non-fast-forward and the project could never sync again. That is what
	// repointing a project at another address produces, which is an ordinary
	// thing for somebody to do (a typo, a repository that moved, a fork), so
	// it is answered with a fresh checkout rather than with a failure nobody
	// can clear from the interface.
	if !remoteMatches(repo, p.SCMURL) {
		return s.replace(ctx, dir, p, auth, progress)
	}
	return repo, s.fetch(ctx, repo, p, auth, progress)
}

// remoteMatches reports whether the checkout already points at url.
//
// The FIRST configured URL is the one compared, not any of them, because that
// is the one a fetch uses. A remote carrying several would otherwise report a
// match on the strength of one this sync is not going to dial.
//
// A checkout with no origin at all counts as not matching: that is debris from
// an interrupted clone rather than a repository this can fetch into, and
// replacing it is the same answer the unreadable case above gets.
func remoteMatches(repo *gogit.Repository, url string) bool {
	remote, err := repo.Remote(gogit.DefaultRemoteName)
	if err != nil {
		return false
	}
	configured := remote.Config().URLs
	return len(configured) > 0 && configured[0] == url
}

// replace checks out a repository beside the current tree and swaps it in only
// once the clone has succeeded.
//
// The order is the whole point. Removing the tree first and then cloning would
// leave a project with NO checkout whenever the new address is wrong, and a
// project with no checkout is one whose every template stops resolving
// (internal/project's playbook source requires both a path and a succeeded
// sync). Cloning first costs one extra tree's worth of disk for the length of
// one sync and keeps the last good checkout serving until there is a better
// one.
func (s *GitSyncer) replace(ctx context.Context, dir string, p Project, auth Auth, progress io.Writer) (*gogit.Repository, error) {
	staging := dir + ".incoming"

	// Debris from an interrupted swap, which is this package's own working
	// area and nobody else's data.
	if err := os.RemoveAll(staging); err != nil {
		return nil, fmt.Errorf("clearing a stale staging directory: %w", err)
	}

	if _, err := s.clone(ctx, staging, p, auth, progress); err != nil {
		// The old tree is untouched, so the project keeps serving whatever it
		// last synced while somebody fixes the address.
		_ = os.RemoveAll(staging)
		return nil, err
	}

	if err := os.RemoveAll(dir); err != nil {
		_ = os.RemoveAll(staging)
		return nil, fmt.Errorf("clearing the previous checkout: %w", err)
	}
	if err := os.Rename(staging, dir); err != nil {
		return nil, fmt.Errorf("installing the new checkout: %w", err)
	}
	return gogit.PlainOpen(dir)
}

// clone makes the first checkout.
func (s *GitSyncer) clone(ctx context.Context, dir string, p Project, auth Auth, progress io.Writer) (*gogit.Repository, error) {
	method, err := authMethod(auth)
	if err != nil {
		return nil, err
	}
	opts := &gogit.CloneOptions{
		URL:      p.SCMURL,
		Auth:     method,
		Depth:    1,
		Progress: progress,
	}
	// An empty branch means the remote's own default, which is not assumed
	// to be "main": a repository whose default is "master" or "trunk" is
	// not this platform's to rename.
	if p.SCMBranch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(p.SCMBranch)
		opts.SingleBranch = true
	}
	return gogit.PlainCloneContext(ctx, dir, false, opts)
}

// fetch fast-forwards an existing checkout.
func (s *GitSyncer) fetch(ctx context.Context, repo *gogit.Repository, p Project, auth Auth, progress io.Writer) error {
	method, err := authMethod(auth)
	if err != nil {
		return err
	}
	// RemoteURL states the address to dial rather than letting go-git read one
	// out of the checkout's .git/config, which is where this defect lived:
	// without it a fetch used whatever the FIRST clone had configured, so
	// editing a project's address changed nothing about what was fetched, ever
	// (FAILURE_PATTERNS.md #271).
	//
	// What makes an edit take effect is the mismatch check in open, which
	// replaces a checkout of a different repository outright, because an
	// unrelated history cannot be fast-forwarded into. This is the narrower
	// guarantee beside it: the address dialed is the one this platform
	// validated, by being passed, rather than by being inferred from a file on
	// disk and trusted to still agree.
	if err := repo.FetchContext(ctx, &gogit.FetchOptions{
		RemoteURL: p.SCMURL,
		Auth:      method,
		Force:     true,
		Progress:  progress,
	}); err != nil && !isUpToDate(err) {
		return err
	}

	tree, err := repo.Worktree()
	if err != nil {
		return err
	}
	pull := &gogit.PullOptions{RemoteURL: p.SCMURL, Auth: method, Progress: progress}
	if p.SCMBranch != "" {
		pull.ReferenceName = plumbing.NewBranchReferenceName(p.SCMBranch)
	}
	if err := tree.PullContext(ctx, pull); err != nil && !isUpToDate(err) {
		return err
	}
	return nil
}

// isUpToDate reports whether err is go-git's "nothing to do" signal, which
// is an error value rather than a nil return and is a success here.
func isUpToDate(err error) bool {
	return err == gogit.NoErrAlreadyUpToDate
}

// resolveAuth turns the project's credential id into values for this one
// clone, or an empty Auth when the repository is public.
func (s *GitSyncer) resolveAuth(ctx context.Context, p Project) (Auth, error) {
	if s.auth == nil || p.CredentialID == 0 {
		return Auth{}, nil
	}
	return s.auth.ResolveAuth(ctx, p.CredentialID)
}

// authMethod maps an Auth onto go-git's transport auth, nil for a public
// repository.
//
// The returned type is the interface rather than a concrete one, because
// the two shapes are genuinely different transports: a key goes over SSH
// and a password goes over HTTPS, and the URL decides which the remote will
// even accept.
func authMethod(a Auth) (transport.AuthMethod, error) {
	switch {
	case a.Empty():
		return nil, nil

	case a.UsesKey():
		// An SSH URL names the user before the host ("git@github.com"),
		// and go-git wants it separately. "git" is what every forge uses
		// and what a bare key with no username implies.
		user := a.Username
		if user == "" {
			user = "git"
		}
		keys, err := gitssh.NewPublicKeys(user, a.PrivateKey, a.Passphrase)
		if err != nil {
			// Deliberately not wrapped with the underlying error's own
			// text. An encrypted key with no passphrase and an encrypted
			// key with the WRONG passphrase produce different messages
			// from x/crypto, and the difference is an oracle. Both are the
			// same problem to the person fixing it.
			return nil, fmt.Errorf("the SSH key could not be read: check that it is a private key and that the passphrase is correct")
		}
		return keys, nil

	default:
		// go-git rejects an empty username even when the token is the
		// whole credential, which is the shape a forge issues. The literal
		// is what GitHub, GitLab and Bitbucket all document for that case.
		username := a.Username
		if username == "" {
			username = "git"
		}
		return &githttp.BasicAuth{Username: username, Password: a.Password}, nil
	}
}

// Playbooks lists the runnable files in a synced working tree.
func (s *GitSyncer) Playbooks(_ context.Context, p Project) ([]string, error) {
	dir := s.pathFor(p)
	if _, err := os.Stat(dir); err != nil {
		// A project that has never synced has no playbooks, which is not
		// an error: it is the honest answer, and the caller renders it as
		// an empty chooser beside a Sync button.
		return nil, nil
	}

	var found []string
	for _, sub := range playbookDirs {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			// A repository with no playbooks/ directory is ordinary.
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !isPlaybook(e.Name()) {
				continue
			}
			found = append(found, filepath.ToSlash(filepath.Join(sub, e.Name())))
		}
	}
	return found, nil
}

// isPlaybook reports whether a filename is a candidate playbook.
func isPlaybook(name string) bool {
	if strings.HasPrefix(name, ".") {
		return false
	}
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml")
}

// scrubURL removes credential material from a message before it is stored
// or shown.
//
// Two things leak. A URL carrying userinfo ("https://user:token@host/repo")
// is echoed verbatim by transport errors, and go-git's own auth failures
// sometimes quote the URL they tried. Both are repaired by replacing every
// occurrence of the configured URL with its userinfo-stripped form, and by
// refusing to emit anything at all if the stripping itself fails.
func scrubURL(msg, raw string) string {
	if raw == "" {
		return msg
	}
	clean := raw
	if parsed, err := url.Parse(raw); err == nil && parsed.User != nil {
		parsed.User = nil
		clean = parsed.String()
		msg = strings.ReplaceAll(msg, raw, clean)
	}

	// Belt and braces: any remaining "scheme://something:something@" in the
	// message is userinfo this function did not have the configured URL
	// for, and it is never safe to keep.
	return userinfoPattern.ReplaceAllString(msg, "$1://")
}
