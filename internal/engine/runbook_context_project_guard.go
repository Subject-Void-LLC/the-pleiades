// The project files a method may never reach through sdk.ProjectFiles or
// sdk.ProjectFileWriter, matched by what they are rather than by how a path
// spells them.
package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// guardedPath is one file or directory a project path may not be, or (for
// a directory) lead into.
type guardedPath struct {
	// rel is its path relative to the project directory, slash-separated,
	// or "" when it lies outside the project.
	rel string
	// info is the file or directory itself, nil while it does not exist.
	info fs.FileInfo
	// what names it in a refusal.
	what string
	// holdsProject says it is the project directory or one holding it, so
	// every path in the project is inside it.
	holdsProject bool
}

// secretsGuard is the project's .pleiades directory, which no method reads
// or writes as a file. A missing one is guarded by name alone.
func secretsGuard(root *os.Root) (guardedPath, error) {
	g := guardedPath{rel: projectSecretsDir, what: "the project's " + projectSecretsDir + " directory, which holds its credentials"}
	info, err := root.Lstat(projectSecretsDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return g, fmt.Errorf("reading the project's %s directory: %w", projectSecretsDir, err)
	default:
		g.info = info
	}
	return g, nil
}

// writeGuards are the paths a write may not replace or write into besides
// .pleiades: each of guarded (absolute, or relative to dir), as the command
// line names them through WithProjectFiles. A guard is read through its
// links, since a guarded directory reached through a link is still the
// directory a write must not reach.
func writeGuards(dir string, guarded []string) ([]guardedPath, error) {
	project, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving the project directory: %w", err)
	}
	out := make([]guardedPath, 0, len(guarded))
	for _, g := range guarded {
		abs := g
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(project, g)
		}
		gp := guardedPath{what: g + ", which a run never writes"}
		// A guarded directory holding the whole project (an external
		// Collections directory set to the project itself) holds every path
		// a write could name.
		if up, err := filepath.Rel(abs, project); err == nil && up != ".." && !strings.HasPrefix(up, ".."+string(filepath.Separator)) {
			gp.holdsProject = true
		}
		if rel, err := filepath.Rel(project, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			gp.rel = filepath.ToSlash(rel)
			// Named as the project names it, the way the path was given.
			gp.what = gp.rel + ", which a run never writes"
		}
		info, err := os.Stat(abs)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, fmt.Errorf("reading %s: %w", g, err)
		default:
			gp.info = info
		}
		out = append(out, gp)
	}
	return out, nil
}

// refuseGuarded refuses clean, a path projectRelative accepted, when it or
// any directory on the way to it is one of guards. Each prefix that exists
// is compared by identity (os.SameFile), so no other spelling of a guarded
// directory reaches it: another letter case on a file system that ignores
// case, a Windows 8.3 short name such as PLEIAD~1, or a trailing dot or
// space Windows drops. Each prefix is also compared by name, ignoring case,
// so a guard that does not exist yet is not created through the write.
// name is the path as given, for the refusal.
func refuseGuarded(root *os.Root, name, clean string, guards []guardedPath) error {
	for _, g := range guards {
		if g.holdsProject {
			return fmt.Errorf("%s: the project directory is inside %s, so no file in it is written", name, g.what)
		}
	}
	parts := strings.Split(clean, "/")
	exists := true
	for i := range parts {
		prefix := strings.Join(parts[:i+1], "/")
		var info fs.FileInfo
		if exists {
			got, err := root.Lstat(prefix)
			if err != nil {
				// Nothing below a missing directory exists either. Any other
				// failure (a component that is not a directory, one the
				// account cannot search) reaches nothing below it, and the
				// caller's own walk of the same path reports it in its own
				// words, so the name check alone goes on here.
				exists = false
			} else {
				info = got
			}
		}
		for _, g := range guards {
			if (info != nil && g.info != nil && os.SameFile(info, g.info)) || (g.rel != "" && strings.EqualFold(prefix, g.rel)) {
				return fmt.Errorf("%s is %s", name, guardedWhere(prefix, clean, g))
			}
		}
	}
	return nil
}

// guardedWhere words a refusal: the guard itself, or a path inside it.
func guardedWhere(prefix, clean string, g guardedPath) string {
	if prefix == clean {
		return g.what
	}
	return "inside " + g.what
}
