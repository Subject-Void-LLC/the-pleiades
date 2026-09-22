//go:build integration

// This file builds "the previous release" of the controller, which the
// upgrade gates start first and then upgrade from.
//
// There are no release tags yet, so the previous release is defined by git:
//
//   - PLEIADES_PREVIOUS_REF, when set, names it outright;
//   - otherwise it is where this branch left main (git merge-base HEAD main),
//     which is what a deployment built from main is running today;
//   - and where that merge base is HEAD itself, it is HEAD when the working
//     tree holds uncommitted changes (they are the build under test), and
//     HEAD's first parent on a clean tree (main before the change HEAD made).
//
// When releases are tagged (the 1.0.0 golden image onward), the newest tag
// before HEAD becomes the answer and the merge-base default goes: it is the
// one part of this file that exists only because there are no releases yet.
//
// The tree is extracted with git archive into a temporary directory, not
// checked out with git worktree: a worktree registers itself in .git, and a
// test that crashes leaves it registered. The build is stamped with the
// commit it came from, so its own `version` (where it has one) and its
// heartbeat say which build it is.
package e2e

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// previousBuild is the previous release's controller, built once per test
// binary.
type previousBuild struct {
	// ref is the commit it was built from.
	ref string

	// tree is the extracted source tree.
	tree string

	// controller is the built binary.
	controller string

	// crossed lists the PostgreSQL migrations this build has and the
	// previous one does not: the schema change an upgrade from it applies.
	crossed []string
	// phase84 says the previous release has Phase 84's rollback window.
	// Only such a build can set aside a database a newer build migrated,
	// which is the first thing its restore does. TRANSITIONAL: every
	// previous release is one once main carries Phase 84, and the field goes
	// at 1.0 gold with the other allowances for older builds.
	phase84 bool
}

var (
	previousOnce  sync.Once
	previous      previousBuild
	previousErr   error
	previousClean []string
)

// requirePreviousBuild returns the previous release's controller, building it
// on first use. It fails rather than skips when the previous release cannot be
// resolved or built: an upgrade gate that quietly skipped would read as one
// that passed.
func requirePreviousBuild(tb testing.TB) previousBuild {
	tb.Helper()
	previousOnce.Do(func() { previous, previousErr = buildPrevious() })
	if previousErr != nil {
		tb.Fatalf("building the previous release: %v", previousErr)
	}
	return previous
}

// buildPrevious resolves, extracts and builds the previous release.
func buildPrevious() (previousBuild, error) {
	root, err := gitOutput("", "rev-parse", "--show-toplevel")
	if err != nil {
		return previousBuild{}, err
	}
	ref, err := previousRef(root)
	if err != nil {
		return previousBuild{}, err
	}

	tree, err := os.MkdirTemp("", "pleiades-previous-")
	if err != nil {
		return previousBuild{}, err
	}
	previousClean = append(previousClean, tree)
	if err := extract(root, ref, tree); err != nil {
		return previousBuild{}, err
	}

	bin := filepath.Join(tree, "controller-previous")
	// #nosec G204 -- go, with arguments this file builds from a commit id
	// git itself resolved.
	build := exec.Command("go", "build", "-o", bin,
		"-ldflags", "-X github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo.revision="+ref,
		"./cmd/controller")
	build.Dir = tree
	if out, err := build.CombinedOutput(); err != nil {
		return previousBuild{}, fmt.Errorf("building %s: %v\n%s", ref, err, out)
	}

	crossed, err := crossedMigrations(root, tree)
	if err != nil {
		return previousBuild{}, err
	}
	// Phase 84's compatibility table is the marker: a build without it has no
	// rollback window and a restore that refuses a newer build's tables.
	_, statErr := os.Stat(filepath.Join(tree, "internal", "ent", "migrate", "compat.go"))
	return previousBuild{ref: ref, tree: tree, controller: bin, crossed: crossed, phase84: statErr == nil}, nil
}

// previousRef resolves the previous release to a full commit id, by the rule
// in this file's header.
func previousRef(root string) (string, error) {
	candidate := os.Getenv("PLEIADES_PREVIOUS_REF")
	if candidate == "" {
		head, err := gitOutput(root, "rev-parse", "HEAD")
		if err != nil {
			return "", err
		}
		base, err := gitOutput(root, "merge-base", "HEAD", "main")
		if err != nil {
			return "", fmt.Errorf("finding where this branch left main (set PLEIADES_PREVIOUS_REF to name the previous release): %w", err)
		}
		candidate = base
		if base == head {
			// This branch has no commits of its own. If the tree holds
			// uncommitted work, that work is the build under test and HEAD
			// is what it would replace; a clean tree on main is the change
			// HEAD made, and its first parent is what that replaced.
			dirty, err := gitOutput(root, "status", "--porcelain", "--untracked-files=no")
			if err != nil {
				return "", err
			}
			if dirty == "" {
				candidate = "HEAD^1"
			}
		}
	}
	// --verify with ^{commit} both checks the name and refuses anything that
	// is not a commit; --end-of-options keeps a hostile name from being read
	// as a flag.
	return gitOutput(root, "rev-parse", "--verify", "--end-of-options", candidate+"^{commit}")
}

// extract writes ref's tree into dir through git archive, reading the
// archive in this process so no shell is involved and no path in it can
// escape dir.
func extract(root, ref, dir string) error {
	// #nosec G204 -- git, with a commit id git itself resolved.
	cmd := exec.Command("git", "archive", "--format=tar", ref)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	tr := tar.NewReader(out)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading the archive of %s: %w", ref, err)
		}
		target := filepath.Join(dir, filepath.Clean("/"+hdr.Name))
		// The archive's own modes, as a checkout has them (0755 directories,
		// 0644 or 0755 files), and not tighter: the compose stack bind-mounts
		// directories of this tree into containers that run as another uid,
		// and a 0750 directory there is one the controller cannot read.
		mode := os.FileMode(hdr.Mode) & 0o755
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, mode|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o600)
			if err != nil {
				return err
			}
			// #nosec G110 -- the archive is this repository's own tree.
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// The tree has none that the build needs; skipped rather than
			// followed, so the extraction cannot point outside dir.
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git archive %s: %v %s", ref, err, stderr.String())
	}
	return nil
}

// crossedMigrations lists the PostgreSQL migrations in root that the tree at
// previous does not have.
func crossedMigrations(root, previous string) ([]string, error) {
	rel := filepath.Join("internal", "ent", "migrate", "migrations", "postgres")
	have := map[string]bool{}
	old, err := os.ReadDir(filepath.Join(previous, rel))
	if err != nil {
		return nil, err
	}
	for _, e := range old {
		have[e.Name()] = true
	}
	now, err := os.ReadDir(filepath.Join(root, rel))
	if err != nil {
		return nil, err
	}
	var crossed []string
	for _, e := range now {
		if !have[e.Name()] {
			crossed = append(crossed, e.Name())
		}
	}
	sort.Strings(crossed)
	return crossed, nil
}

// gitOutput runs git in dir and returns its trimmed output.
func gitOutput(dir string, args ...string) (string, error) {
	// #nosec G204 -- git, with arguments this file chooses.
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, exit.Stderr)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// removePreviousBuilds deletes every tree buildPrevious extracted. TestMain
// calls it after the suite.
func removePreviousBuilds() {
	for _, dir := range previousClean {
		_ = os.RemoveAll(dir)
	}
}
