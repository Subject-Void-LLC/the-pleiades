// Package playbook resolves and lists the unconverted Ansible playbooks a
// deployment carries, by id, under one fixed directory.
//
// It is the playbook-kind twin of internal/runbook: the same id grammar,
// the same "<id>.yml or <id>.yaml directly under the root" resolution, the
// same fail-closed constructor, and the same List-for-catalog method. The
// symmetry is deliberate, because the two feed the same surfaces: the
// launch catalog offers both kinds' definitions from one picker, and the
// dispatch worker verifies both kinds' definitions at fan-out.
//
// It lives here rather than in internal/adapters/legacy, where the
// resolution logic first shipped, because of who has to import it. The
// legacy package's non-test files import testcontainers-go for container
// orchestration (FAILURE_PATTERNS.md #109), so a Controller that imported
// it to list playbooks would link a testing library and a Docker client
// into the control plane binary for the sake of a readdir. This package
// imports the standard library and nothing else.
package playbook

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNotFound is returned when a reference is well-formed but names no
// playbook file under the configured root.
var ErrNotFound = errors.New("playbook: not found")

// maxReferenceLength bounds a playbook reference. Paths, unlike ids, are
// legitimately long: "network/edge/tripplite_python/tripplite_config.yml"
// is an ordinary one.
const maxReferenceLength = 1024

// ValidateReference reports whether ref is a playbook reference this
// source could resolve: a project-relative, forward-slashed path to a
// YAML file that stays inside the project it belongs to.
//
// A PATH, not an id, and that is the whole point of this function. Real
// Ansible projects put playbooks in subdirectories, and AWX's Playbook
// field is populated with exactly these strings from a scan of the
// project's tree: "tripplite_python/tripplite_config.yml" is what a
// production job template stores. A grammar of bare ids cannot express
// that, and a platform positioned as an Ansible superset that cannot name
// the customer's own playbook has not imported anything.
//
// This restores the shape the launch kind validated before a earlier pass
// in this session collapsed the two disagreeing grammars onto the WRONG
// one. There were two statements of this contract, one accepting paths
// and one accepting flat ids, and the resolution kept the flat ids: the
// half that was internally tidy and externally useless. The lesson is in
// FAILURE_PATTERNS.md #113 and it is not "state a contract once", which
// was already the rule; it is that reconciling two statements is a
// decision about which one is RIGHT, and correctness is decided by the
// system being compatible with, never by which side is simpler to
// enforce.
//
// Shape only, and the shape carries the security boundary. The reference
// reaches filepath.Join in Get below, and is supplied by whoever may
// create a template, so an absolute path or a traversal would read any
// file the process can reach (FAILURE_PATTERNS.md #78 is the same class
// at a different boundary). Confinement is enforced twice: lexically
// here, and again against the resolved absolute path in Get.
func ValidateReference(ref string) error {
	raw := strings.TrimSpace(ref)
	switch {
	case raw == "":
		return fmt.Errorf("a playbook reference is empty")
	case len(raw) > maxReferenceLength:
		return fmt.Errorf("playbook path is longer than %d characters", maxReferenceLength)
	case strings.ContainsRune(raw, 0):
		return fmt.Errorf("playbook path contains a null byte")
	case strings.HasPrefix(raw, "/"):
		return fmt.Errorf("playbook path %q is absolute: a playbook is named relative to its project", raw)
	case strings.Contains(raw, `\`):
		return fmt.Errorf("playbook path %q uses backslashes: paths here are forward-slashed on every platform", raw)
	}

	// Cleaned and compared rather than searched for "..", so a directory
	// whose name merely contains two dots is allowed while one that
	// actually climbs is not.
	cleaned := path.Clean(raw)
	if cleaned != raw {
		return fmt.Errorf("playbook path %q is not in its simplest form: write it as %q", raw, cleaned)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("playbook path %q climbs out of its project", raw)
	}

	if ext := path.Ext(cleaned); ext != ".yml" && ext != ".yaml" {
		// Refused rather than assumed, because the alternative is handing
		// ansible-playbook a file that is not a playbook and reporting
		// whatever it says about it as a job failure.
		return fmt.Errorf("playbook path %q does not name a YAML file", raw)
	}
	return nil
}

// ValidReference is ValidateReference as a predicate, for callers that
// only need the yes or no.
func ValidReference(ref string) bool { return ValidateReference(ref) == nil }

// skippedDirs are the conventional Ansible role-layout directories a
// playbook never sits directly in, skipped when walking a tree so the
// picker built from List offers playbooks rather than every YAML file in
// the repository.
//
// A convention, applied honestly: this package does not parse a file to
// confirm it is a play list (AWX does), so what List returns is "the YAML
// files in the places playbooks live". Skipping these is the difference
// between a usable dropdown and one listing four hundred vars files.
var skippedDirs = map[string]bool{
	"roles": true, "group_vars": true, "host_vars": true, "vars": true,
	"defaults": true, "tasks": true, "handlers": true, "meta": true,
	"templates": true, "files": true, "library": true,
	"filter_plugins": true, "lookup_plugins": true, "module_utils": true,
	"collections": true, "molecule": true, "tests": true,
}

// Source resolves a playbook id to the raw bytes of the playbook it
// names, and lists every id it could resolve.
//
// A distinct port from internal/runbook.Source, even though the contract
// rhymes, because the output types differ structurally: a runbook resolves
// to a compiled *engine.DAG, a playbook to bytes handed verbatim to a real
// ansible-playbook process, never parsed by this codebase.
type Source interface {
	// Get resolves id to its playbook's raw bytes, or an error satisfying
	// errors.Is(err, ErrNotFound) if id names no playbook here.
	Get(ctx context.Context, id string) ([]byte, error)

	// List returns every playbook id this Source can resolve, sorted. Ids
	// rather than contents, so opening a catalog costs one readdir.
	List(ctx context.Context) ([]string, error)
}

// DirSource is the filesystem-backed Source, resolving id to a
// "<id>.yml" or "<id>.yaml" file under a fixed root directory.
type DirSource struct {
	dir string
}

// NewDirSource builds a DirSource rooted at dir, fail-closed at
// construction if dir does not exist or is not readable, the same
// startup-time-not-request-time check internal/runbook.NewDirSource
// establishes for the identical reason: a misconfigured playbook
// directory is an operator error to catch immediately, not an opaque
// failure on a dispatched job's first request.
func NewDirSource(dir string) (*DirSource, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("playbook directory is not accessible: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("playbook directory path %q is not a directory", dir)
	}
	if _, err := os.ReadDir(dir); err != nil {
		return nil, fmt.Errorf("playbook directory is not readable: %w", err)
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve playbook directory to an absolute path: %w", err)
	}
	return &DirSource{dir: absDir}, nil
}

// Get resolves a project-relative reference to its playbook's raw bytes.
// Unlike internal/runbook.DirSource.Get, this deliberately caches nothing:
// there is no compile step here whose cost a cache would amortize (a
// legacy playbook is handed to a real ansible-playbook process as-is), so
// a cache would only add staleness risk for no benefit.
func (d *DirSource) Get(ctx context.Context, ref string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Reject before any path is built, identical ordering to
	// internal/runbook.DirSource's own resolve: ref is caller-controlled
	// (it crosses the wire as wire.DispatchPayload.RunbookID).
	if err := ValidateReference(ref); err != nil {
		return nil, err
	}

	absPath, err := d.resolve(strings.TrimSpace(ref))
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- absPath is built from a reference already validated
	// by ValidateReference (relative, forward-slashed, lexically clean,
	// no climb, YAML extension) and re-verified by resolve to land inside
	// d.dir, mirroring internal/runbook/dir_source.go's own identical
	// #nosec justification for the identical trust boundary.
	data, err := os.ReadFile(absPath)
	if err == nil {
		return data, nil
	}
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, ref)
	}
	return nil, fmt.Errorf("failed to read playbook %q: %w", ref, err)
}

// List walks the tree and returns every playbook reference under it,
// project-relative and sorted: "site.yml",
// "tripplite_python/tripplite_config.yml".
//
// A walk rather than one readdir, because that is where playbooks
// actually live. A real project groups them into per-integration
// directories, and the flat listing this replaced could not see a single
// one of them, which made the picker built on it empty for every
// repository a customer would recognise.
//
// Conventional role-layout directories are skipped (see skippedDirs) and
// so are dot-directories, most importantly ".git": walking a synced
// checkout's object store is both useless and slow. A reference that
// would not survive Get is skipped rather than returned, so nothing is
// offered as runnable that cannot be run.
func (d *DirSource) List(_ context.Context) ([]string, error) {
	var refs []string

	err := filepath.WalkDir(d.dir, func(abs string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(d.dir, abs)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		name := entry.Name()

		if entry.IsDir() {
			if strings.HasPrefix(name, ".") || skippedDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}

		// Slash-separated on every platform, because the reference is a
		// stored value that crosses a wire and a database, not a local
		// path: a template authored on one host must resolve on another.
		ref := filepath.ToSlash(rel)
		if ValidReference(ref) {
			refs = append(refs, ref)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list playbook directory: %w", err)
	}

	sort.Strings(refs)
	return refs, nil
}

// resolve builds the absolute path for a validated reference and
// defensively re-verifies it is still inside d.dir.
//
// Belt and suspenders, exactly like internal/runbook/dir_source.go's own
// resolve, and it carries more weight here than it did over a flat id
// grammar: this grammar admits separators, so lexical validation is doing
// real work rather than being redundant with a character class. The check
// is against the resolved absolute path, so it holds even if the
// validation above is ever loosened.
func (d *DirSource) resolve(ref string) (string, error) {
	joined := filepath.Join(d.dir, filepath.FromSlash(ref))
	absPath, err := filepath.Abs(filepath.Clean(joined))
	if err != nil {
		return "", fmt.Errorf("failed to resolve playbook path: %w", err)
	}
	if !strings.HasPrefix(absPath, d.dir+string(filepath.Separator)) {
		return "", fmt.Errorf("resolved playbook path escapes the playbook directory")
	}
	return absPath, nil
}
