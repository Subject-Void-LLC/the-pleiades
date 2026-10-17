// A run's project files, read by a method through sdk.ProjectFiles on
// the machine that holds the project: the command line.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// projectSecretsDir is the project's own directory of credentials and the
// master key, which no method reads as a file.
const projectSecretsDir = ".pleiades"

// WithProjectFiles makes every context next builds answer sdk.ProjectFiles
// from dir, the project directory. Only the command line wraps its
// contexts so: a Walk-tier Runner holds no project, and its contexts
// answer sdk.ErrNoProjectFiles.
func WithProjectFiles(next RunbookContextFunc, dir string) RunbookContextFunc {
	return func(ctx context.Context, device inventory.InventoryItem) (sdk.RunbookContext, error) {
		rc, err := next(ctx, device)
		if err != nil {
			return nil, err
		}
		if c, ok := rc.(*runbookContext); ok {
			c.projectDir = dir
		}
		return rc, nil
	}
}

// ReadProjectFile implements sdk.ProjectFiles. name is cleaned and must
// stay relative and inside the project; it is opened through an os.Root
// on the project directory, so nothing it names escapes it, and no
// component may be a symbolic link, since a link that stays inside the
// project could still point into .pleiades, which is refused by name
// (case-insensitively: macOS's file system is). The file must be regular
// and at most max bytes, measured and enforced while reading.
func (c *runbookContext) ReadProjectFile(name string, max int64) ([]byte, error) {
	if c.projectDir == "" {
		return nil, sdk.ErrNoProjectFiles
	}
	clean, err := projectRelative(name)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(c.projectDir)
	if err != nil {
		return nil, fmt.Errorf("opening the project directory: %w", err)
	}
	defer func() { _ = root.Close() }()

	parts := strings.Split(clean, "/")
	for i := range parts {
		info, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s passes through a symbolic link (%s); name the file itself", name, strings.Join(parts[:i+1], "/"))
		}
	}
	f, err := root.Open(clean)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	if info.Size() > max {
		return nil, fmt.Errorf("%s is %d bytes, over the %d one read takes", name, info.Size(), max)
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s grew past the %d bytes one read takes while it was read", name, max)
	}
	return data, nil
}

// projectRelative is name cleaned, refused when it is empty, absolute,
// climbs out of the project, or reaches its .pleiades directory.
func projectRelative(name string) (string, error) {
	if name == "" {
		return "", errors.New("names no file")
	}
	if strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || (len(name) > 1 && name[1] == ':') {
		return "", fmt.Errorf("%s is not a path relative to the project directory", name)
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%s leaves the project directory", name)
	}
	for _, part := range strings.Split(clean, "/") {
		if strings.EqualFold(part, projectSecretsDir) {
			return "", fmt.Errorf("%s is inside the project's %s directory, which holds its credentials and is never read as a file", name, projectSecretsDir)
		}
	}
	return clean, nil
}
