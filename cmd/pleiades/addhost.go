package main

import (
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/classification"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
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
		return fmt.Errorf("--set cannot write property %s: only onboarding writes it (pleiades onboard)", key)
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

// splitPositional pulls a command's single positional argument (add-host's
// host name, and, since Phase 33, forge new-device's vendor and forge
// new-collection's namespaced name) out of args, wherever the user placed
// it, and returns the remaining tokens for flag.FlagSet to parse. The
// stdlib flag package stops parsing flags at the first non-flag token,
// which would otherwise reject PLAN.md Section 7's own documented usage,
// `add-host webserver1 --type linux`, where the name comes first.
//
// boolFlags names every flag (with or without its leading dashes) that
// takes no following value in its bare form, matching the stdlib flag
// package's own bool-flag convention (`-flag` means true; `-flag=value` is
// also accepted, but `-flag value` is two separate arguments, the second
// of which is NOT the flag's value). Every other "-"-prefixed token is
// assumed to consume the next token as its value, so boolFlags is the only
// thing standing between a bare boolean flag and a token that follows it
// being wrongly swallowed as that flag's value -- or, worse, being missed
// entirely and misread as a second, disallowed positional argument. Pass
// nil when a command defines no boolean flags (every flag add-host itself
// defines takes a value).
func splitPositional(args []string, boolFlags map[string]bool) (positional string, rest []string, err error) {
	positionals, rest := splitPositionals(args, boolFlags)
	if len(positionals) > 1 {
		return "", nil, fmt.Errorf("unexpected extra argument: %q", positionals[1])
	}
	if len(positionals) == 0 {
		return "", nil, errMissingPositional
	}
	return positionals[0], rest, nil
}

// splitPositionals is splitPositional for a command that takes any number
// of positional arguments (validate's runbooks, which a shell glob such as
// runbooks/*.yaml expands into several). It returns every non-flag token
// in the order given, and the flag tokens for flag.FlagSet to parse,
// reading boolFlags exactly as splitPositional does.
func splitPositionals(args []string, boolFlags map[string]bool) (positionals, rest []string) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			// "--flag=value" carries its value in the same token; a bare
			// "--flag value" does not, so the next token belongs to it --
			// unless flag is itself boolean, which takes no following
			// value in its bare form.
			hasInlineValue := strings.Contains(a, "=")
			flagName := strings.TrimLeft(strings.SplitN(a, "=", 2)[0], "-")
			if !hasInlineValue && !boolFlags[flagName] && i+1 < len(args) {
				i++
				rest = append(rest, args[i])
			}
			continue
		}
		positionals = append(positionals, a)
	}
	return positionals, rest
}

// errMissingPositional is splitPositional's error for args with no
// positional argument, so a command whose positional is optional can
// tell that case from a real parse error.
var errMissingPositional = errors.New("missing positional argument")

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
	name, rest, err := splitPositional(args, nil)
	if err != nil {
		return fmt.Errorf("usage: pleiades add-host <name> (--type <type> | --classify a,b,c) [--set key=value ...] [--tags a,b]: %w", err)
	}

	fs := flag.NewFlagSet("add-host", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	deviceType := fs.String("type", "", "device type (e.g. linux_server, cisco_router)")
	classifyFlag := fs.String("classify", "", "comma-separated classification path (e.g. linux_server,debian_family,ubuntu), an alternative to --type")
	tagsFlag := fs.String("tags", "", "comma-separated tags")
	props := &keyValueList{}
	fs.Var(props, "set", "device property as key=value (repeatable)")

	if err := fs.Parse(rest); err != nil {
		return err
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

	var tags []string
	if *tagsFlag != "" {
		tags = strings.Split(*tagsFlag, ",")
	}

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
