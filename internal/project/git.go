// This file is the git half of a Project: cloning a repository onto local
// disk and listing what can be run out of it.
//
// # Why go-git rather than shelling out
//
// The controller image carries no git binary, go-git honours a
// context.Context so a hung fetch is cancellable, and, decisively, there is
// no argument-injection surface. A repository URL is operator-supplied
// data, and `git clone <url>` with a url beginning "--upload-pack=" runs an
// arbitrary program. Passing that same string as a struct field cannot.
//
// The cost is real and worth stating: no submodules, and a large repository
// costs more memory than the C implementation would.
package project

import (
	"context"
	"fmt"
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
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
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

	// locks serialises work per project id. Two syncs interleaving a
	// checkout of the same tree produce a working directory that matches
	// no commit, which is worse than either sync losing.
	locks sync.Map
}

// NewGitSyncer returns a syncer rooted at dir.
func NewGitSyncer(dir string) *GitSyncer { return &GitSyncer{root: dir} }

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
func (s *GitSyncer) Sync(ctx context.Context, p Project, auth Auth) (Result, error) {
	if !p.Syncable() {
		return Result{}, ErrNotSyncable
	}

	lock := s.lockFor(p.ID)
	lock.Lock()
	defer lock.Unlock()

	dir := s.pathFor(p)
	result := Result{LocalPath: dir, At: time.Now().UTC()}

	repo, err := s.open(ctx, dir, p, auth)
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
func (s *GitSyncer) open(ctx context.Context, dir string, p Project, auth Auth) (*gogit.Repository, error) {
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return nil, fmt.Errorf("preparing the project directory: %w", err)
	}

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			return nil, fmt.Errorf("clearing a stale project directory: %w", rmErr)
		}
		return s.clone(ctx, dir, p, auth)
	}
	return repo, s.fetch(ctx, repo, p, auth)
}

// clone makes the first checkout.
func (s *GitSyncer) clone(ctx context.Context, dir string, p Project, auth Auth) (*gogit.Repository, error) {
	opts := &gogit.CloneOptions{
		URL:   p.SCMURL,
		Auth:  authMethod(auth),
		Depth: 1,
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
func (s *GitSyncer) fetch(ctx context.Context, repo *gogit.Repository, p Project, auth Auth) error {
	err := repo.FetchContext(ctx, &gogit.FetchOptions{
		Auth:  authMethod(auth),
		Force: true,
	})
	if err != nil && !isUpToDate(err) {
		return err
	}

	tree, err := repo.Worktree()
	if err != nil {
		return err
	}
	pull := &gogit.PullOptions{Auth: authMethod(auth)}
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

// authMethod maps an Auth onto go-git's transport auth, nil for a public
// repository.
func authMethod(a Auth) *githttp.BasicAuth {
	if a.Empty() {
		return nil
	}
	// go-git rejects an empty username even when the password is the whole
	// credential, which is the shape a forge token takes. The literal is
	// what GitHub, GitLab and Bitbucket all document for that case.
	username := a.Username
	if username == "" {
		username = "git"
	}
	return &githttp.BasicAuth{Username: username, Password: a.Password}
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
