// Where the Crawl CLI keeps devices' facts (Phase 41): .pleiades/facts in the
// project, beside the journal.
package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// factsDir is the project's fact store directory.
func factsDir(projectDir string) string {
	return filepath.Join(projectDir, ".pleiades", "facts")
}

// factKeysToWrite is the keys a real run seals the project's facts under
// (internal/facts' atrest.go), derived from the project's master key, which
// is read only when a fact is stored. A project with no key gets one then,
// as its credentials would, but only while its store holds no fact: one that
// stored facts and lost its key is refused, naming the key to restore, since
// a new key beside them would seal new facts under it while every old one
// stays sealed under the key that is gone, leaving a store no one key reads.
// A store that cannot be looked at is refused the same way.
//
// credential.ResolveMasterKey, the one function that makes a key, holds every
// writer to the same rule over what the vault's .pleiades holds sealed (its
// credentials, named secrets, seal key and, when the vault is the project, these
// facts), so no command makes a key beside any of it (FAILURE_PATTERNS 1344).
// The check here runs first only to say it in the facts' own words; without it
// ResolveMasterKey would refuse the same write. It is no guard for a --vault
// elsewhere: that flag refuses a vault with no key before any command runs.
func factKeysToWrite(projectDir string) *facts.Keys {
	return facts.NewLazyKeys(func() ([]byte, error) { return factMasterKeyToWrite(projectDir) }, "project")
}

// factMasterKeyToWrite is the project's master key for a write of its facts
// (factKeysToWrite, and the keyer of a private file's digest): the key it
// has, or a new one only while its store holds no fact.
func factMasterKeyToWrite(projectDir string) ([]byte, error) {
	key, err := credential.ExistingMasterKey(vaultDir(projectDir))
	if !errors.Is(err, credential.ErrNoMasterKey) {
		return key, err
	}
	store, openErr := facts.OpenFileStore(factsDir(projectDir), nil)
	switch {
	case errors.Is(openErr, facts.ErrNoStore):
	case openErr != nil:
		return nil, fmt.Errorf("the project has no master key, and whether its fact store holds facts sealed under one cannot be told (%w): no new key is made", openErr)
	default:
		holds, holdsErr := store.HoldsAny()
		if holds && holdsErr == nil {
			// A fact is stored only under a key that exists, so one stored since
			// the look above (another command, or this run's other keys, making
			// the project's first key) comes with its key: take it.
			if key, err := credential.ExistingMasterKey(vaultDir(projectDir)); !errors.Is(err, credential.ErrNoMasterKey) {
				return key, err
			}
		}
		if holds || holdsErr != nil {
			return nil, fmt.Errorf("the project's stored facts are sealed under a master key it no longer has: restore .pleiades/master.key or set PLEIADES_MASTER_KEY; no fact is stored until then, and no new key is made beside them (%w)", err)
		}
	}
	return credential.ResolveMasterKey(vaultDir(projectDir))
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

// projectFactStore is what a run in projectDir can read of its devices'
// stored facts, for validation (validate.FactsRule), and why when the key is
// there and cannot be used: readable when the project has the master key
// every stored fact is sealed under; no key when it has none, whether no fact
// was ever stored or the key that sealed them is gone; and unusable, with the
// key's own refusal (a key file another account may read or replace, a value
// that is not a key), which names a file or a variable and never a key. A run
// that gathers then reads in the same run needs no key; one reading what an
// earlier run stored does.
func projectFactStore(projectDir string) (validate.FactStore, string) {
	_, err := credential.ExistingMasterKey(vaultDir(projectDir))
	switch {
	case err == nil:
		return validate.FactStoreReadable, ""
	case errors.Is(err, credential.ErrNoMasterKey):
		return validate.FactStoreNoKey, ""
	}
	return validate.FactStoreKeyUnreadable, err.Error()
}
