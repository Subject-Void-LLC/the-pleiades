// Package main: --vault and --inventory, which move the credential vault and
// the inventory file away from --dir, each for its own part only.
//
// Every command reads its project from --dir (default "."). A runbook kept in
// one folder run against credentials kept in another failed with "credential
// is empty" unless --dir moved both. --vault names the directory whose
// .pleiades/ holds everything secret or derived from the master key: master.key,
// credentials.yaml with its named secrets, the seal key, and so the facts digest
// key derived from the master key (owner decision 2026-11-13, "every secret
// moves"). The journal, the fact store, run.lock and the rest of .pleiades/
// stay under --dir. --inventory names the inventory file (Ansible's -i is the
// precedent); group_vars/ and host_vars/ are read beside it.
//
// One process runs one command, so the two values are held here once, set by
// the command's own flag parse, and every site that used to join --dir with
// a vault or inventory path asks vaultDir or inventoryFile instead.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// masterKeyEnv is the variable that supplies the master key in place of a
// key file (internal/credential's masterKeyEnvVar).
const masterKeyEnv = "PLEIADES_MASTER_KEY"

// vaultFlagDoc and inventoryFlagDoc are the two flags' help, shared with
// internal/clispec's entries word for word.
const (
	vaultFlagDoc     = "directory whose .pleiades/ holds the credential vault (master.key, credentials.yaml, seal.key), in place of --dir's; the journal and facts stay under --dir"
	inventoryFlagDoc = "inventory file to read and write, in place of --dir's inventory.yaml; group_vars/ and host_vars/ are read beside it"
)

// vaultOverride and inventoryOverride are --vault and --inventory as the
// running command parsed them, absolute, or empty when not given.
var vaultOverride, inventoryOverride string

// addVaultFlag registers --vault on flags. Given, the directory must exist and
// its .pleiades/ must hold a master key, unless PLEIADES_MASTER_KEY supplies
// one: a --vault with no key is a mistyped path far more often than a new
// vault, and making a key there would leave every secret elsewhere unreadable.
// The key file itself is held to the private-file check when it is read.
func addVaultFlag(flags *flag.FlagSet) {
	registerVaultFlag(flags, true)
}

// addKeylessVaultFlag registers --vault for a command that only removes an
// entry from credentials.yaml (remove-host, remove-credential): removing
// decrypts nothing, so no master key is required in the vault, though the
// file is still held to the private-file check when it is read.
func addKeylessVaultFlag(flags *flag.FlagSet) {
	registerVaultFlag(flags, false)
}

// registerVaultFlag registers --vault on flags, requiring a master key in it
// when needsKey is set.
func registerVaultFlag(flags *flag.FlagSet, needsKey bool) {
	flags.Func("vault", vaultFlagDoc, func(value string) error {
		if value == "" {
			return errors.New("--vault needs a directory")
		}
		abs, err := filepath.Abs(value)
		if err != nil {
			return err
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return fmt.Errorf("--vault %s is not a directory", value)
		}
		if _, set := os.LookupEnv(masterKeyEnv); needsKey && !set {
			key := filepath.Join(abs, ".pleiades", "master.key")
			if _, err := os.Stat(key); errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("--vault %s holds no master key (%s): name the directory whose .pleiades/ holds the vault, or set %s", value, key, masterKeyEnv)
			}
		}
		vaultOverride = abs
		return nil
	})
}

// addInventoryFlag registers --inventory on flags.
func addInventoryFlag(flags *flag.FlagSet) {
	flags.Func("inventory", inventoryFlagDoc, func(value string) error {
		if value == "" {
			return errors.New("--inventory needs a file")
		}
		abs, err := filepath.Abs(value)
		if err != nil {
			return err
		}
		inventoryOverride = abs
		return nil
	})
}

// addProjectFlags registers both, for a command that reads the inventory
// and the vault.
func addProjectFlags(flags *flag.FlagSet) {
	addVaultFlag(flags)
	addInventoryFlag(flags)
}

// vaultDir returns the directory whose .pleiades/ holds the vault: --vault
// when given, else dir.
func vaultDir(dir string) string {
	if vaultOverride != "" {
		return vaultOverride
	}
	return dir
}

// inventoryFile returns the inventory file: --inventory when given, else
// dir's inventory.yaml.
func inventoryFile(dir string) string {
	if inventoryOverride != "" {
		return inventoryOverride
	}
	return filepath.Join(dir, inventory.DefaultInventoryFilename)
}
