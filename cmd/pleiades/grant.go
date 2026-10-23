// Command pleiades's `grant-capability` and `revoke-capability`
// subcommands: an operator's explicit, recorded assertion that a device
// holds a capability an external program defines, and taking one back.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// granting is the one write path a grant has on an inventory item
// (record.Base.RecordGrant and RevokeGrant).
type granting interface {
	RecordGrant(inventory.Grant) error
	RevokeGrant(capability.Name) (bool, error)
}

// runGrantCapability asserts that a device holds a capability a loaded
// external program defines, with the grant's properties and a reason.
//
// It is the one way a person grants a capability, and it says so in the
// record: the grant is an assertion, with who made it, when and why, never
// a probe's finding. It refuses a capability compiled into The Pleiades,
// which only onboarding grants (a compiled capability is a promise the
// device type's own code keeps), a capability no loaded program defines,
// properties that do not fit the definition, and a grant the device cannot
// hold because it lacks a compiled capability the definition requires.
func runGrantCapability(args []string) error {
	positional, rest := splitPositionals(args, nil)
	if len(positional) != 2 {
		return errors.New("usage: pleiades grant-capability <device> <capability> --reason <text> [--set key=value ...]")
	}
	name, capName := positional[0], capability.Name(positional[1])
	fs := flag.NewFlagSet("grant-capability", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	reason := fs.String("reason", "", "why this device holds the capability, recorded with the grant (required)")
	set := &keyValueList{}
	fs.Var(set, "set", "grant property as key=value (repeatable)")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if err := inventory.ValidateGrantReason(*reason); err != nil {
		return fmt.Errorf("--reason: %w", err)
	}
	if !capability.IsDataName(capName) {
		if _, compiled := capability.Lookup(capName); compiled {
			return fmt.Errorf("%s is compiled into The Pleiades, and only onboarding grants a compiled capability (pleiades onboard %s)", capName, name)
		}
		return fmt.Errorf("%q is not the name of a capability an external program defines (<namespace>.<name>)", capName)
	}

	ctx := context.Background()
	if _, err := loadExternalCollections(ctx, *dir); err != nil {
		return err
	}
	def, ok := capability.DataOf(capName)
	if !ok {
		return fmt.Errorf("no loaded program defines %s: set %s to the directory holding the program that does", capName, collectionsDirEnv)
	}
	props := map[string]any{}
	for k, v := range set.values {
		props[k] = v
	}
	if err := capability.ValidateGrant(def, props); err != nil {
		return err
	}

	factory := inv.NewItemFactory()
	repo := inv.NewFileRepository(filepath.Join(*dir, inv.DefaultInventoryFilename), factory)
	item, err := repo.GetByName(ctx, name)
	if err != nil {
		return err
	}
	g, ok := item.(granting)
	if !ok {
		return fmt.Errorf("host %q cannot hold a granted capability", name)
	}
	if err := g.RecordGrant(inventory.Grant{
		Capability: capName,
		Source:     inventory.GrantAsserted,
		Properties: props,
		By:         operatorName(),
		Reason:     *reason,
		At:         time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("host %q: %w", name, err)
	}
	rebuilt, err := factory.Rebuilt(item)
	if err != nil {
		return fmt.Errorf("host %q not changed: %w", name, err)
	}
	if !rebuilt.HasCapability(capName) {
		return fmt.Errorf("host %q not changed: it cannot hold %s, which requires %s beside it", name, capName, requirementsText(def))
	}
	if err := repo.Save(ctx, item); err != nil {
		return err
	}
	fmt.Printf("granted host %q %s, asserted by %s: %s\n", name, capName, operatorName(), *reason)
	return nil
}

// runRevokeCapability removes a device's grant of a capability, however it
// was made.
func runRevokeCapability(args []string) error {
	positional, rest := splitPositionals(args, nil)
	if len(positional) != 2 {
		return errors.New("usage: pleiades revoke-capability <device> <capability>")
	}
	name, capName := positional[0], capability.Name(positional[1])
	fs := flag.NewFlagSet("revoke-capability", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	ctx := context.Background()
	factory := inv.NewItemFactory()
	repo := inv.NewFileRepository(filepath.Join(*dir, inv.DefaultInventoryFilename), factory)
	item, err := repo.GetByName(ctx, name)
	if err != nil {
		return err
	}
	g, ok := item.(granting)
	if !ok {
		return fmt.Errorf("host %q holds no granted capability", name)
	}
	removed, err := g.RevokeGrant(capName)
	if err != nil {
		return fmt.Errorf("host %q: %w", name, err)
	}
	if !removed {
		return fmt.Errorf("host %q has no grant of %s", name, capName)
	}
	if err := factory.Rebuild(item); err != nil {
		return fmt.Errorf("host %q not changed: %w", name, err)
	}
	if err := repo.Save(ctx, item); err != nil {
		return err
	}
	fmt.Printf("revoked %s from host %q\n", capName, name)
	return nil
}

// operatorName is who the grant records as asserting it: the account
// running this command.
func operatorName() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "unknown"
}

// requirementsText names the compiled capabilities def requires.
func requirementsText(def capability.DataDefinition) string {
	names := make([]string, 0, len(def.Requires))
	for _, r := range def.Requires {
		names = append(names, string(r))
	}
	if len(names) == 0 {
		return "nothing"
	}
	return strings.Join(names, ", ")
}
