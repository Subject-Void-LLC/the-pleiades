// The commands that take a project's inventory apart again: remove-host and
// remove-credential, the counterparts of add-host and add-credential.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

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
	// HostKeysNote says why --forget-host-keys had nothing to forget: the
	// host is reached through a console server, whose keys are the server's.
	HostKeysNote string `json:"host_keys_note,omitempty"`
}

// runRemoveHost removes one host from the project's inventory, and with
// flags its stored credential and the known_hosts lines trusting its SSH
// address and its NETCONF listener's. The journal keeps every run that touched it.
func runRemoveHost(args []string) error {
	fs := flag.NewFlagSet("remove-host", flag.ContinueOnError)
	// Removing a credential decrypts nothing, so the vault needs no key.
	addInventoryFlag(fs)
	addKeylessVaultFlag(fs)
	dir := fs.String("dir", ".", "project directory")
	withCredential := fs.Bool("credential", false, "also remove the host's stored credential")
	forgetKeys := fs.Bool("forget-host-keys", false, "also remove the known_hosts lines trusting the host's SSH address and its NETCONF listener's, each unless another device is reached there")
	leaveRefs := fs.Bool("leave-references", false, "remove the host even when another host names it in a property")
	asJSON := fs.Bool("json", false, "print one JSON document instead of text")
	name, err := parseOneArg(fs, args)
	if err != nil {
		return fmt.Errorf("%s: %w", removeHostUsage, err)
	}

	hostsPath := inventoryFile(*dir)
	out := removedHost{Host: name}
	// The addresses are read before the host is gone, and each is kept
	// only when no other device is reached at it.
	var addresses []string
	if *forgetKeys {
		var toForget forgetPlan
		if toForget, err = addressesToForget(*dir, hostsPath, name); err != nil {
			return err
		}
		addresses, out.HostKeysKept, out.HostKeysNote = toForget.addresses, toForget.kept, toForget.note
	}

	spec, err := inv.RemoveHost(hostsPath, name, *leaveRefs)
	if err != nil {
		return err
	}
	out.ID, out.Type = spec.ID, spec.Type
	if *withCredential {
		if out.CredentialRemoved, err = credential.RemoveFileStore(vaultDir(*dir), name); err != nil {
			return fmt.Errorf("host %s was removed, but not its credential: %w", name, err)
		}
	}
	if len(addresses) > 0 {
		path, err := remoteexec.KnownHostsFile()
		if err != nil {
			return err
		}
		for _, address := range addresses {
			removed, err := hosttrust.Remove(path, address)
			if err != nil {
				return fmt.Errorf("host %s was removed, but not its host keys at %s: %w", name, address, err)
			}
			out.HostKeysRemoved += removed
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
	if out.HostKeysKept != "" {
		fmt.Println(termsafe.EscapeLine(out.HostKeysKept))
	}
	if out.HostKeysNote != "" {
		fmt.Println(termsafe.EscapeLine(out.HostKeysNote))
	}
	if len(addresses) > 0 {
		fmt.Printf("removed %d known_hosts line(s) for %s\n", out.HostKeysRemoved, termsafe.EscapeLine(strings.Join(addresses, " and ")))
	}
	return nil
}

// runRemoveCredential removes one device's stored credential, leaving the
// device itself in the inventory.
func runRemoveCredential(args []string) error {
	fs := flag.NewFlagSet("remove-credential", flag.ContinueOnError)
	// Removing a credential decrypts nothing, so the vault needs no key.
	addKeylessVaultFlag(fs)
	dir := fs.String("dir", ".", "project directory")
	name, err := parseOneArg(fs, args)
	if err != nil {
		return fmt.Errorf("usage: pleiades remove-credential <device> [--dir DIR]: %w", err)
	}
	removed, err := credential.RemoveFileStore(vaultDir(*dir), name)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no credential is stored for %s", termsafe.EscapeLine(name))
	}
	fmt.Printf("removed the credential of %s\n", termsafe.EscapeLine(name))
	return nil
}

// forgetPlan is what remove-host --forget-host-keys drops for one host.
type forgetPlan struct {
	// addresses are those whose known_hosts lines go.
	addresses []string
	// kept says which addresses stayed and why, empty when none did.
	kept string
	// note says why there was nothing to forget at all, empty otherwise.
	note string
}

// addressesToForget returns the addresses whose known_hosts lines
// remove-host --forget-host-keys drops for the host name: its SSH address
// and its NETCONF listener's, less each one another device in the project is
// also reached at, whose keys must stay trusted.
//
// A host with no SSH address (a node reached only through a console server)
// has no keys of its own to forget: the keys trust-host trusted for it are the
// console server's, which other nodes still reach. That is a note and not a
// refusal, so the host itself is still removed.
func addressesToForget(dir, hostsPath, name string) (forgetPlan, error) {
	repo := inv.NewFileRepository(hostsPath, inv.NewItemFactory())
	device, err := repo.GetByName(context.Background(), name)
	if err != nil {
		return forgetPlan{}, err
	}
	mine, err := trustedAddresses(device)
	if errors.Is(err, errNotReachedOverSSH) {
		return forgetPlan{note: fmt.Sprintf("%s is not reached over SSH at an address, so it has no host keys of its own to forget", name)}, nil
	}
	if err != nil {
		return forgetPlan{}, fmt.Errorf("--forget-host-keys: %w", err)
	}
	others, err := loadInventory(dir)
	if err != nil {
		return forgetPlan{}, err
	}
	// Every address another device is reached at, with the first such
	// device, so a shared one can be named.
	sharedWith := map[string]string{}
	for _, other := range others {
		if other.Name() == name {
			continue
		}
		// A device not reached over SSH has no address to share.
		theirs, err := trustedAddresses(other)
		if err != nil {
			continue
		}
		for _, a := range theirs {
			if _, seen := sharedWith[a]; !seen {
				sharedWith[a] = other.Name()
			}
		}
	}
	var plan forgetPlan
	var reasons []string
	for _, address := range mine {
		if other, shared := sharedWith[address]; shared {
			reasons = append(reasons, fmt.Sprintf("%s is also reached at %s, so its host keys stay trusted", other, address))
			continue
		}
		plan.addresses = append(plan.addresses, address)
	}
	plan.kept = strings.Join(reasons, "; ")
	return plan, nil
}
