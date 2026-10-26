// The commands that take a project's inventory apart again: remove-host and
// remove-credential, the counterparts of add-host and add-credential.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/hosttrust"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// removeHostUsage is remove-host's usage line.
const removeHostUsage = "usage: pleiades remove-host <name> [--credential] [--forget-host-keys] [--leave-references] [--dir DIR] [--json]"

// removedHost is what remove-host did, its --json view.
type removedHost struct {
	Host              string `json:"host"`
	ID                string `json:"id"`
	Type              string `json:"type"`
	CredentialRemoved bool   `json:"credential_removed"`
	HostKeysRemoved   int    `json:"host_keys_removed"`
	HostKeysKept      string `json:"host_keys_kept,omitempty"`
}

// runRemoveHost removes one host from the project's inventory, and with
// flags its stored credential and the known_hosts lines trusting its SSH
// address. The journal keeps every run that touched it.
func runRemoveHost(args []string) error {
	name, rest, err := splitPositional(args, map[string]bool{"credential": true, "forget-host-keys": true, "leave-references": true, "json": true})
	if err != nil {
		return fmt.Errorf("%s: %w", removeHostUsage, err)
	}
	fs := flag.NewFlagSet("remove-host", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	withCredential := fs.Bool("credential", false, "also remove the host's stored credential")
	forgetKeys := fs.Bool("forget-host-keys", false, "also remove the known_hosts lines trusting the host's SSH address, unless another device is reached there")
	leaveRefs := fs.Bool("leave-references", false, "remove the host even when another host names it in a property")
	asJSON := fs.Bool("json", false, "print one JSON document instead of text")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New(removeHostUsage)
	}

	hostsPath := filepath.Join(*dir, inv.DefaultInventoryFilename)
	out := removedHost{Host: name}
	// The address is read before the host is gone, and kept only when no
	// other device is reached at it.
	var address string
	if *forgetKeys {
		repo := inv.NewFileRepository(hostsPath, inv.NewItemFactory())
		device, err := repo.GetByName(context.Background(), name)
		if err != nil {
			return err
		}
		if address, err = sshAddress(device); err != nil {
			return fmt.Errorf("--forget-host-keys: %w", err)
		}
		others, err := loadInventory(*dir)
		if err != nil {
			return err
		}
		for _, other := range others {
			if other.Name() == name {
				continue
			}
			if a, err := sshAddress(other); err == nil && a == address {
				out.HostKeysKept = fmt.Sprintf("%s is also reached at %s, so its host keys stay trusted", other.Name(), address)
				address = ""
				break
			}
		}
	}

	spec, err := inv.RemoveHost(hostsPath, name, *leaveRefs)
	if err != nil {
		return err
	}
	out.ID, out.Type = spec.ID, spec.Type
	if *withCredential {
		if out.CredentialRemoved, err = credential.RemoveFileStore(*dir, name); err != nil {
			return fmt.Errorf("host %s was removed, but not its credential: %w", name, err)
		}
	}
	if address != "" {
		path, err := remoteexec.KnownHostsFile()
		if err != nil {
			return err
		}
		if out.HostKeysRemoved, err = hosttrust.Remove(path, address); err != nil {
			return fmt.Errorf("host %s was removed, but not its host keys: %w", name, err)
		}
	}

	if *asJSON {
		return writeJSON(os.Stdout, out)
	}
	fmt.Printf("removed host %s (%s) from %s\n", termsafe.EscapeLine(name), termsafe.EscapeLine(spec.Type), hostsPath)
	if *withCredential {
		if out.CredentialRemoved {
			fmt.Println("removed its credential")
		} else {
			fmt.Println("it had no stored credential")
		}
	}
	switch {
	case out.HostKeysKept != "":
		fmt.Println(termsafe.EscapeLine(out.HostKeysKept))
	case *forgetKeys:
		fmt.Printf("removed %d known_hosts line(s) for %s\n", out.HostKeysRemoved, termsafe.EscapeLine(address))
	}
	return nil
}

// runRemoveCredential removes one device's stored credential, leaving the
// device itself in the inventory.
func runRemoveCredential(args []string) error {
	name, rest, err := splitPositional(args, nil)
	if err != nil {
		return fmt.Errorf("usage: pleiades remove-credential <device> [--dir DIR]: %w", err)
	}
	fs := flag.NewFlagSet("remove-credential", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: pleiades remove-credential <device> [--dir DIR]")
	}
	removed, err := credential.RemoveFileStore(*dir, name)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no credential is stored for %s", termsafe.EscapeLine(name))
	}
	fmt.Printf("removed the credential of %s\n", termsafe.EscapeLine(name))
	return nil
}
