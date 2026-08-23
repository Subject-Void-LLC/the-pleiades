package inventory

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultInventoryFilename is the static YAML inventory file every
// scaffolded project starts with.
const DefaultInventoryFilename = "inventory.yaml"

// DefaultRunbookDir is where scaffolded runbooks live.
const DefaultRunbookDir = "runbooks"

// DefaultSampleRunbook is the starter runbook Scaffold writes.
const DefaultSampleRunbook = "sample.yaml"

const starterInventory = `# Pleiades static inventory (Crawl tier: no server, no database, no broker).
# Add hosts by hand below, or run: pleiades add-host <name> --type <type>
hosts: []
`

const starterRunbook = `# Sample runbook. Run it with: pleiades run runbooks/sample.yaml
id: sample
tasks:
  - name: check
    fqcn: noop
`

const starterReadme = "This project was created by `pleiades init`.\n\n" +
	"Next steps:\n" +
	"  pleiades add-host <name> --type linux_server --set host=<ip>\n" +
	"  pleiades add-credential <name> --username <user>   # for ssh_exec tasks; prompts for a password\n" +
	"  pleiades validate\n" +
	"  pleiades run runbooks/sample.yaml\n"

// Scaffold creates a new Crawl-tier project in dir: a static inventory
// file, a starter runbook, and a short README. It never overwrites a
// file that already exists, so re-running init in an existing project
// directory is safe and just fills in whatever is missing.
func Scaffold(dir string) ([]string, error) {
	var created []string

	inventoryPath := filepath.Join(dir, DefaultInventoryFilename)
	wrote, err := writeIfAbsent(inventoryPath, starterInventory)
	if err != nil {
		return created, err
	}
	if wrote {
		created = append(created, inventoryPath)
	}

	runbookDir := filepath.Join(dir, DefaultRunbookDir)
	// 0o755, not 0o750: this is a normal, non-secret project directory
	// (runbooks), meant to be as shareable and version-controllable as
	// any other project file, unlike the credential/master-key
	// directories elsewhere in this codebase, which already use 0o700.
	if err := os.MkdirAll(runbookDir, 0o755); err != nil { // #nosec G301 -- intentional, see comment above
		return created, fmt.Errorf("failed to create %s: %w", runbookDir, err)
	}

	runbookPath := filepath.Join(runbookDir, DefaultSampleRunbook)
	wrote, err = writeIfAbsent(runbookPath, starterRunbook)
	if err != nil {
		return created, err
	}
	if wrote {
		created = append(created, runbookPath)
	}

	readmePath := filepath.Join(dir, "README.md")
	wrote, err = writeIfAbsent(readmePath, starterReadme)
	if err != nil {
		return created, err
	}
	if wrote {
		created = append(created, readmePath)
	}

	return created, nil
}

// writeIfAbsent writes content to path only if no file exists there yet.
// It reports whether it actually wrote, so Scaffold can tell the caller
// what changed on disk.
func writeIfAbsent(path, content string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("failed to stat %s: %w", path, err)
	}

	// 0o644, not 0o600: every file Scaffold writes (hosts.yaml, a starter
	// runbook, README.md) is meant to be a normal, hand-editable,
	// shareable project file, not a secret.
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { // #nosec G306 -- intentional, see comment above
		return false, fmt.Errorf("failed to write %s: %w", path, err)
	}
	return true, nil
}
