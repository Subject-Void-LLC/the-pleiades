package legacy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// validPlaybookID mirrors internal/runbook's own validRunbookID exactly:
// same character class, same length bound, same "reject before any path
// construction" position (checked first in Get, below, before id ever
// reaches filepath.Join). It is a separate value, not an import, because
// internal/runbook.DirSource.Get returns a compiled *engine.DAG from the
// native runbook YAML grammar, structurally unusable for a raw Ansible
// playbook file: this package needs its own resolver, not that one's
// output type, even though the safety property both enforce is
// identical.
var validPlaybookID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ErrPlaybookNotFound is returned when id is well-formed but names no
// playbook file under the configured directory.
var ErrPlaybookNotFound = errors.New("legacy: playbook not found")

// PlaybookSource resolves a dispatched wire.DispatchPayload.RunbookID to
// the raw bytes of the legacy Ansible playbook it names. A distinct port
// from internal/runbook.Source (which resolves the same kind of id to a
// compiled *engine.DAG): which one a given RunbookID is resolved through
// depends entirely on which ExecutionAdapter the Runner process is
// composed with, exactly as runtime adapter selection remains out of
// scope for this phase (cmd/runner/main.go's own comment).
type PlaybookSource interface {
	Get(ctx context.Context, id string) ([]byte, error)
}

// DirPlaybookSource is the filesystem-backed PlaybookSource
// implementation, resolving id to a "<id>.yml" or "<id>.yaml" file under
// a fixed root directory.
type DirPlaybookSource struct {
	dir string
}

// NewDirPlaybookSource builds a DirPlaybookSource rooted at dir,
// fail-closed at construction if dir does not exist or is not readable,
// the same startup-time-not-request-time check
// internal/runbook.NewDirSource already establishes for the identical
// reason: a misconfigured playbook directory is an operator error to
// catch immediately, not an opaque failure on a dispatched job's first
// request.
func NewDirPlaybookSource(dir string) (*DirPlaybookSource, error) {
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
	return &DirPlaybookSource{dir: absDir}, nil
}

// Get resolves id to its playbook's raw bytes. Unlike
// internal/runbook.DirSource.Get, this deliberately caches nothing: there
// is no compile step here whose cost a cache would amortize (a legacy
// playbook is handed to a real ansible-playbook process as-is, never
// parsed by this codebase), so a cache would only add staleness risk for
// no benefit.
func (d *DirPlaybookSource) Get(ctx context.Context, id string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Reject before any path is built, identical ordering to
	// internal/runbook.DirSource's own resolve: id is caller-controlled
	// (it crosses the wire as wire.DispatchPayload.RunbookID), so the
	// rejection message below names neither id nor any path.
	if !validPlaybookID.MatchString(id) {
		return nil, fmt.Errorf("invalid playbook id: must match %s", validPlaybookID.String())
	}

	for _, ext := range []string{".yml", ".yaml"} {
		absPath, err := d.resolvePath(id, ext)
		if err != nil {
			return nil, err
		}
		// #nosec G304 -- absPath is built from an id already validated
		// against validPlaybookID (letters, digits, hyphens, underscores
		// only, 1-64 characters) and re-verified by resolvePath to
		// resolve lexically inside d.dir, mirroring
		// internal/runbook/dir_source.go's own identical #nosec
		// justification for the identical trust boundary.
		data, err := os.ReadFile(absPath)
		if err == nil {
			return data, nil
		}
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to read playbook %q: %w", id, err)
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrPlaybookNotFound, id)
}

// resolvePath builds the candidate path for id+ext and defensively
// re-verifies it is still lexically inside d.dir, belt-and-suspenders
// exactly like internal/runbook/dir_source.go's own resolve: given
// validPlaybookID above already excludes "/", "\", and ".", this check
// should be structurally unreachable, and is kept anyway because it costs
// nothing and does not depend on validPlaybookID never changing in some
// way that reopens this gap in the future.
func (d *DirPlaybookSource) resolvePath(id, ext string) (string, error) {
	joined := filepath.Join(d.dir, id+ext)
	cleaned := filepath.Clean(joined)
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve playbook path: %w", err)
	}
	if !strings.HasPrefix(absPath, d.dir+string(filepath.Separator)) {
		return "", fmt.Errorf("resolved playbook path escapes the playbook directory")
	}
	return absPath, nil
}
