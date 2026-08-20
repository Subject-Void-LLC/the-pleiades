// Command pleiades's `forge new-filter` subcommand lives here: thin flag
// parsing that delegates all real generation work to
// internal/forge/filterscaffold, per Phase 30's "forge subcommand files own
// no business logic beyond their own flags" convention.
package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/filterscaffold"
)

// paramFlag accumulates repeated --param flags into an ordered
// []filterscaffold.Param, the same "flag.Value collecting into a slice"
// shape the standard library itself documents as the way to accept a
// flag more than once; forge's other subcommands have not needed this
// yet because none of them take an ordered, arbitrary-length argument
// list the way a filter's own parameter list is.
type paramFlag []filterscaffold.Param

func (p *paramFlag) String() string {
	if p == nil {
		return ""
	}
	parts := make([]string, len(*p))
	for i, param := range *p {
		parts[i] = param.Name + ":" + param.GoType
	}
	return strings.Join(parts, ",")
}

// Set parses one --param value: "name:goType" or, when goType is not one
// of filterscaffold's well-known types (string, int, bool),
// "name:goType:celType" naming the raw cel-go type expression explicitly.
func (p *paramFlag) Set(value string) error {
	parts := strings.SplitN(value, ":", 3)
	if len(parts) < 2 {
		return fmt.Errorf("--param %q: want name:goType or name:goType:celType", value)
	}
	param := filterscaffold.Param{Name: parts[0], GoType: parts[1]}
	if len(parts) == 3 {
		param.CELType = parts[2]
	}
	*p = append(*p, param)
	return nil
}

// parseReturnFlag parses --return "goType" or --return "goType:celType"
// into a filterscaffold.Return.
func parseReturnFlag(value string) filterscaffold.Return {
	parts := strings.SplitN(value, ":", 2)
	ret := filterscaffold.Return{GoType: parts[0]}
	if len(parts) == 2 {
		ret.CELType = parts[1]
	}
	return ret
}

// runForgeNewFilter emits a new pkg/filters function and its starter
// test, plus (printed last, per viewscaffold.Reminder's own precedent)
// the CEL registration block a human pastes into
// internal/engine/cel_filters.go. See internal/forge/filterscaffold for
// the actual generation logic, and PLAN.md Section 36 for what a filter
// is and is not.
func runForgeNewFilter(args []string) error {
	usage := "usage: pleiades forge new-filter <GoName> --cel-name <celName> --category <name> --summary <text> " +
		"--param name:goType[:celType] [--param ...] --return goType[:celType] [--skip-existing] [--dir .]"

	goName, rest, err := splitPositional(args, map[string]bool{"skip-existing": true})
	if err != nil {
		return fmt.Errorf("%s: %w", usage, err)
	}

	fs := flag.NewFlagSet("forge new-filter", flag.ContinueOnError)
	dir := fs.String("dir", ".", "repository directory to write the generated files into")
	celName := fs.String("cel-name", "", "the bare name after \"filters.\" in a runbook condition, e.g. cidrToNetmask")
	category := fs.String("category", "", "PLAN.md Section 36 category this filter belongs to, e.g. network")
	summary := fs.String("summary", "", "one sentence describing what this filter does")
	var params paramFlag
	fs.Var(&params, "param", "one argument: name:goType, or name:goType:celType for a type filterscaffold does not know")
	ret := fs.String("return", "", "this filter's result: goType, or goType:celType for a type filterscaffold does not know")
	skipExisting := fs.Bool("skip-existing", false, "leave an already-written file alone instead of refusing")

	if err := fs.Parse(rest); err != nil {
		return err
	}
	if *ret == "" {
		return fmt.Errorf("%s: --return is required", usage)
	}

	cfg := filterscaffold.Config{
		GoName:   goName,
		CELName:  *celName,
		Category: *category,
		Summary:  *summary,
		Params:   []filterscaffold.Param(params),
		Return:   parseReturnFlag(*ret),
	}

	files, err := filterscaffold.Generate(cfg)
	if err != nil {
		return err
	}

	if *skipExisting {
		relPaths := make([]string, len(files))
		for i, f := range files {
			relPaths[i] = f.Path
		}
		existing, err := firstExistingFile(*dir, relPaths)
		if err != nil {
			return err
		}
		if existing != "" {
			fmt.Printf("skipped %q (%s already exists)\n", goName, existing)
			return nil
		}
	}

	for _, f := range files {
		written, err := writeGeneratedFile(*dir, f.Path, f.Content)
		if err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", written)
	}

	reminder, err := filterscaffold.Reminder(cfg)
	if err != nil {
		return err
	}
	// Printed last, and at length, because it is the one step no
	// generator can perform (internal/engine/cel_filters.go is one
	// hand-maintained file, not a directory a blank import can wire in)
	// and the only one whose omission produces no error at all: a
	// filter generated but never registered compiles, tests clean by
	// itself, and is simply never reachable from any when_cel
	// expression.
	fmt.Printf("\n%q is not yet reachable from filters.*: paste the block below into\n", goName)
	fmt.Println("internal/engine/cel_filters.go's filtersLibrary.CompileOptions, then implement")
	fmt.Printf("pkg/filters.%s for real.\n\n", goName)
	fmt.Print(reminder)
	return nil
}
