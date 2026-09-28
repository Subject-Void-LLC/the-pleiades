// Command pleiades's `set-host` subcommand: change an existing host's
// properties. It parses flags and delegates the write to the inventory
// repository, so each change is a revision in the host's history, as a
// change from any other writer is.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// keyList accumulates repeated --unset flags, refusing the one property
// only onboarding writes, as keyValueList does for --set.
type keyList struct {
	keys []string
}

func (k *keyList) String() string { return strings.Join(k.keys, ",") }

func (k *keyList) Set(s string) error {
	if s == "" {
		return errors.New("--unset expects a property name")
	}
	if inventory.IsReservedProperty(s) {
		return fmt.Errorf("--unset cannot remove property %s: only onboarding writes it (pleiades onboard)", s)
	}
	k.keys = append(k.keys, s)
	return nil
}

// runSetHost changes an existing host's properties: --set adds or
// replaces one, --unset removes one.
//
// The device is rebuilt as its type before anything is written, so a
// value the type refuses (a pinned authority on a Windows server's HTTP
// port, a malformed certificate) is refused here rather than on the next
// run. A value equal to the one stored is not a change and records no
// revision. A property named by both flags is refused as a contradiction.
func runSetHost(args []string) error {
	name, rest, err := splitPositional(args, nil)
	if err != nil {
		return fmt.Errorf("usage: pleiades set-host <name> [--set key=value ...] [--unset key ...]: %w", err)
	}
	fs := flag.NewFlagSet("set-host", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	set := &keyValueList{}
	fs.Var(set, "set", "device property to add or replace, as key=value (repeatable)")
	unset := &keyList{}
	fs.Var(unset, "unset", "device property to remove (repeatable)")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if len(set.values)+len(unset.keys) == 0 {
		return errors.New("nothing to change: give --set key=value or --unset key")
	}
	for _, key := range unset.keys {
		if _, both := set.values[key]; both {
			return fmt.Errorf("property %s is both set and unset", key)
		}
	}

	ctx := context.Background()
	factory := inv.NewItemFactory()
	repo := inv.NewFileRepository(filepath.Join(*dir, inv.DefaultInventoryFilename), factory)
	item, err := repo.GetByName(ctx, name)
	if err != nil {
		return err
	}

	current := item.Properties().Raw()
	var changed []string
	keys := make([]string, 0, len(set.values))
	for key := range set.values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if old, exists := current[key]; exists && reflect.DeepEqual(old, set.values[key]) {
			continue
		}
		if err := item.AddInfo(key, set.values[key], true); err != nil {
			return err
		}
		changed = append(changed, "set "+key)
	}
	for _, key := range unset.keys {
		if err := item.RemoveInfo(key); err != nil {
			return fmt.Errorf("host %q: %w", name, err)
		}
		changed = append(changed, "unset "+key)
	}
	if len(changed) == 0 {
		fmt.Printf("host %q unchanged\n", name)
		return nil
	}

	if err := factory.Rebuild(item); err != nil {
		return fmt.Errorf("host %q not changed: %w", name, err)
	}
	if err := repo.Save(ctx, item); err != nil {
		return err
	}
	fmt.Printf("updated host %q: %s\n", name, strings.Join(changed, ", "))
	return nil
}
