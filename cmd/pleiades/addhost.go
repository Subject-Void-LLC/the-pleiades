package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/classification"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/google/uuid"
)

// keyValueList accumulates repeated --set key=value flags into a map. It
// implements flag.Value so flag.FlagSet handles the repetition itself,
// rather than this file hand-parsing a joined string.
type keyValueList struct {
	values map[string]interface{}
}

func (k *keyValueList) String() string {
	if k == nil {
		return ""
	}
	return fmt.Sprintf("%v", k.values)
}

func (k *keyValueList) Set(s string) error {
	key, value, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("--set expects key=value, got %q", s)
	}
	if pkginventory.IsReservedProperty(key) {
		return fmt.Errorf("--set cannot write property %s: only %s writes it", key, pkginventory.ReservedPropertyWriter(key))
	}
	// The vars property is a mapping, which one key=value cannot express.
	if key == pkginventory.VarsProperty {
		return fmt.Errorf("--set cannot write property %s: write variables in group_vars/ or host_vars/ beside %s, or under the host's properties in it", key, inventory.DefaultInventoryFilename)
	}
	if k.values == nil {
		k.values = map[string]interface{}{}
	}
	k.values[key] = parsePropertyValue(value)
	return nil
}

// parsePropertyValue infers a --set value's Go type the same way
// inventory.yaml's own YAML decoder would, rather than always storing a
// raw string. pkg/inventory.Properties' typed accessors (Int, Bool) only
// recognize a value already holding that Go type (Int accepts int or
// float64, Bool accepts bool), so a property set via this flag must
// decode the same way a value hand-authored in inventory.yaml does, or
// every non-string property becomes silently unusable through --set:
// SSHPort, for example, would fail to read a "--set port=2222" value and
// silently fall back to its own default of 22 rather than erroring, since
// Properties.Int("port") is not a value being set as expected, it is a
// value that was never usably set at all (FAILURE_PATTERNS.md #21).
//
// Only exact "true"/"false" become bool, and only a value that parses
// entirely as a base-10 integer becomes int; anything else, including a
// dotted version string like "15.2" or "6.6.87" that would misparse as a
// float, stays a plain string. No float case is added deliberately: this
// property vocabulary has no float-typed property today, and adding one
// would silently corrupt exactly the version-string properties (
// ios_version, kernel_version) that must stay strings.
func parsePropertyValue(raw string) interface{} {
	switch raw {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return n
	}
	return raw
}

// runAddHost appends one host to the static inventory file, generating a
// stable DeviceID so a later rename in the file does not orphan it. The
// actual read-modify-write round-trips through the same YAML layer
// (internal/inventory) that validate and run load from.
//
// Exactly one of --type/--classify is required. --classify is resolved
// against classification.DefaultRuleSet eagerly, here, rather than left
// for hydration time: PLAN.md Architecture Principle 5 ("type safety
// moves left") means an unresolvable classify path should fail this
// command loudly, not silently surface three commands later at validate
// or run. Both the resolved Type and the original Classify are persisted;
// Classify survives only as provenance and is never re-consulted once
// Type is present (see ResolveHostType's doc comment).
func runAddHost(args []string) error {
	fs := flag.NewFlagSet("add-host", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	deviceType := fs.String("type", "", "device type (e.g. linux_server, cisco_router)")
	classifyFlag := fs.String("classify", "", "comma-separated classification path (e.g. linux_server,debian_family,ubuntu), an alternative to --type")
	// --tags and --tag are one flag under two names (set-host spells it --tag),
	// each repeatable and each taking one tag or several separated by commas.
	// An empty value adds nothing, as --tags "" always did.
	addTags := &tagList{flag: "tags"}
	addTag := func(s string) error {
		if s == "" {
			return nil
		}
		return addTags.Set(s)
	}
	fs.Func("tags", "comma-separated tags (repeatable); --tag is the same flag", addTag)
	fs.Func("tag", "a tag, or several separated by commas (repeatable); --tags is the same flag", addTag)
	props := &keyValueList{}
	fs.Var(props, "set", "device property as key=value (repeatable)")

	name, err := parseOneArg(fs, args)
	if err != nil {
		return fmt.Errorf("usage: pleiades add-host <name> (--type <type> | --classify a,b,c) [--set key=value ...] [--tags a,b | --tag a ...]: %w", err)
	}
	// The name becomes the device's address once it reaches a Controller,
	// so "", "." and ".." are refused here, with the reason, before the
	// inventory is read.
	if err := recordname.Check(name); err != nil {
		return fmt.Errorf("host not added: %w", err)
	}

	if (*deviceType == "") == (*classifyFlag == "") {
		return fmt.Errorf("exactly one of --type or --classify is required")
	}

	var classifyPath []string
	if *classifyFlag != "" {
		classifyPath = strings.Split(*classifyFlag, ",")
	}

	resolvedType := *deviceType
	if resolvedType == "" {
		result, err := classification.DefaultRuleSet().Classify(classifyPath)
		if err != nil {
			return fmt.Errorf("--classify %q: %w", *classifyFlag, err)
		}
		if result.Value.Type == nil {
			return fmt.Errorf("--classify %q matched but assigned no type", *classifyFlag)
		}
		resolvedType = *result.Value.Type
	}

	path := filepath.Join(*dir, inventory.DefaultInventoryFilename)
	hosts, err := inventory.ReadHosts(path)
	if err != nil {
		return fmt.Errorf("failed to read inventory (did you run 'pleiades init'?): %w", err)
	}

	for _, h := range hosts {
		if h.Name == name {
			return fmt.Errorf("host %q already exists", name)
		}
	}

	tags := addTags.tags

	spec := inventory.HostSpec{
		ID:         uuid.New().String(),
		Name:       name,
		Type:       resolvedType,
		Classify:   classifyPath,
		Tags:       tags,
		Properties: props.values,
	}
	// Built once as its type before it is written, so a property the type
	// refuses is refused here rather than on the first run.
	if _, err := inventory.NewItemFactory().Build(record.Record{
		ID: pkginventory.DeviceID(spec.ID), Name: name, Type: resolvedType, Properties: spec.Properties,
	}); err != nil {
		return fmt.Errorf("host %q not added: %w", name, err)
	}
	hosts = append(hosts, spec)

	if err := inventory.WriteHosts(path, hosts); err != nil {
		return err
	}

	fmt.Printf("added host %q (%s) to %s\n", name, resolvedType, path)
	return nil
}
