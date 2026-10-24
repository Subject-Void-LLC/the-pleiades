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
		return fmt.Errorf("--unset cannot remove property %s: only %s writes it", s, inventory.ReservedPropertyWriter(s))
	}
	k.keys = append(k.keys, s)
	return nil
}

// tagList accumulates a repeated tag flag, each value one tag or several
// separated by commas, as add-host's --tags takes them.
type tagList struct {
	flag string
	tags []string
}

func (t *tagList) String() string { return strings.Join(t.tags, ",") }

func (t *tagList) Set(s string) error {
	for tag := range strings.SplitSeq(s, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return fmt.Errorf("--%s expects a tag name", t.flag)
		}
		t.tags = append(t.tags, tag)
	}
	return nil
}

// setHostUsage is set-host's synopsis, printed with a positional error.
const setHostUsage = "usage: pleiades set-host <name> [--set key=value ...] [--unset key ...] [--tag tag ...] [--untag tag ...]"

// runSetHost changes an existing host's properties and tags: --set adds
// or replaces a property, --unset removes one, --tag adds a tag and
// --untag removes one.
//
// The flags are parsed before the host name is looked for, so a flag
// set-host does not define is reported as that, rather than as a missing
// name because the unknown flag took the name as its value.
//
// The device is rebuilt as its type before anything is written, so a
// value the type refuses (a pinned authority on a Windows server's HTTP
// port, a malformed certificate) is refused here rather than on the next
// run. A value equal to the one stored is not a change and records no
// revision. A property or tag named by both of its flags is refused as a
// contradiction.
func runSetHost(args []string) error {
	positionals, rest := splitPositionals(args, nil)
	fs := flag.NewFlagSet("set-host", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	set := &keyValueList{}
	fs.Var(set, "set", "device property to add or replace, as key=value (repeatable)")
	unset := &keyList{}
	fs.Var(unset, "unset", "device property to remove (repeatable)")
	tag := &tagList{flag: "tag"}
	fs.Var(tag, "tag", "tag to add, or several separated by commas (repeatable)")
	untag := &tagList{flag: "untag"}
	fs.Var(untag, "untag", "tag to remove, or several separated by commas (repeatable)")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	switch len(positionals) {
	case 0:
		return fmt.Errorf("%s: %w", setHostUsage, errMissingPositional)
	case 1:
	default:
		return fmt.Errorf("%s: unexpected extra argument: %q", setHostUsage, positionals[1])
	}
	name := positionals[0]
	if len(set.values)+len(unset.keys)+len(tag.tags)+len(untag.tags) == 0 {
		return errors.New("nothing to change: give --set key=value, --unset key, --tag tag or --untag tag")
	}
	for _, key := range unset.keys {
		if _, both := set.values[key]; both {
			return fmt.Errorf("property %s is both set and unset", key)
		}
	}
	for _, t := range untag.tags {
		if slices.Contains(tag.tags, t) {
			return fmt.Errorf("tag %s is both tagged and untagged", t)
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
	if len(tag.tags)+len(untag.tags) > 0 {
		tagged, err := changeHostTags(item, tag.tags, untag.tags)
		if err != nil {
			return fmt.Errorf("host %q: %w", name, err)
		}
		changed = append(changed, tagged...)
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

// tagChanger is the mutator a stored device records a tag change through,
// bumping its version and recording a revision as a property change does.
type tagChanger interface {
	ChangeTags(tags []inventory.Tag) bool
}

// changeHostTags adds add to item's tags and removes remove, keeping the
// order the tags already have, and returns what changed as set-host
// reports it. A tag already held, or one to remove that is not held,
// changes nothing.
func changeHostTags(item inventory.InventoryItem, add, remove []string) ([]string, error) {
	changer, ok := item.(tagChanger)
	if !ok {
		return nil, errors.New("this device cannot record a tag change")
	}
	var next []inventory.Tag
	var changed []string
	for _, t := range item.Tags() {
		if slices.Contains(remove, string(t)) {
			changed = append(changed, "untag "+string(t))
			continue
		}
		next = append(next, t)
	}
	for _, t := range add {
		if slices.Contains(next, inventory.Tag(t)) {
			continue
		}
		next = append(next, inventory.Tag(t))
		changed = append(changed, "tag "+t)
	}
	if !changer.ChangeTags(next) {
		return nil, nil
	}
	return changed, nil
}
