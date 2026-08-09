// This file holds the pieces shared by forge_new_device.go and
// forge_new_collection.go: both scaffold packages
// (internal/inventory/devicescaffold, internal/forge/collectionscaffold)
// are pure, I/O-free generators, so the one place that touches disk, and
// the one place a --capabilities flag value gets parsed, lives here
// rather than being duplicated per subcommand.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// writeGeneratedFile writes content to filepath.Join(dir, relPath),
// refusing to overwrite anything that already exists there (naming the
// colliding path) rather than silently skipping it. This deliberately
// diverges from internal/inventory/project.go's Scaffold, which silently
// skips a file that already exists because re-running `pleiades init` in
// an existing project is expected and idempotent: `forge new-*` is a
// different kind of action, an explicit "generate this new, specific
// thing," so a collision is a real problem the caller should see. It
// returns the full path written, for the caller's own success message.
func writeGeneratedFile(dir, relPath string, content []byte) (string, error) {
	full := filepath.Join(dir, relPath)

	if _, err := os.Stat(full); err == nil {
		return "", fmt.Errorf("refusing to overwrite existing file: %s", full)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("checking %s: %w", full, err)
	}

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil { // #nosec G301 -- generated source, not secret material
		return "", fmt.Errorf("creating directory for %s: %w", full, err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil { // #nosec G306 -- generated source, not secret material
		return "", fmt.Errorf("writing %s: %w", full, err)
	}
	return full, nil
}

// parseCapabilitiesFlag splits a comma-separated --capabilities flag value
// into capability.Name values. An empty string yields no capabilities, not
// one containing a single empty Name.
func parseCapabilitiesFlag(flagValue string) []capability.Name {
	if flagValue == "" {
		return nil
	}
	parts := strings.Split(flagValue, ",")
	names := make([]capability.Name, 0, len(parts))
	for _, p := range parts {
		names = append(names, capability.Name(p))
	}
	return names
}
