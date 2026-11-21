// Where the Crawl CLI keeps devices' facts (Phase 41): .pleiades/facts in the
// project, beside the journal.
package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// factsDir is the project's fact store directory.
func factsDir(projectDir string) string {
	return filepath.Join(projectDir, ".pleiades", "facts")
}

// factKeysToWrite is the keys a real run seals the project's facts under
// (internal/facts' atrest.go), derived from the project's master key, which
// is read, and made when the project has none yet, only when a fact is
// stored: the same key and the same rule its credentials have.
func factKeysToWrite(projectDir string) *facts.Keys {
	return facts.NewLazyKeys(func() ([]byte, error) { return credential.ResolveMasterKey(vaultDir(projectDir)) }, "project")
}

// factKeysToRead is the keys a read opens the project's facts with: the same
// ones, from the master key the project already has. A read never makes a
// key, since a new one opens nothing already stored, so a project whose key
// is gone says so.
func factKeysToRead(projectDir string) *facts.Keys {
	return facts.NewLazyKeys(func() ([]byte, error) {
		key, err := credential.ExistingMasterKey(vaultDir(projectDir))
		if errors.Is(err, credential.ErrNoMasterKey) {
			return nil, fmt.Errorf("the project's stored facts are sealed under its master key, and it has none: set PLEIADES_MASTER_KEY or restore .pleiades/master.key (%w)", err)
		}
		return key, err
	}, "project")
}
