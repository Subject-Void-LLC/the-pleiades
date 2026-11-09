// Tests how commands split their arguments into positionals and flags:
// the splitter itself, and every command that uses it, each through its
// own real FlagSet, stopped by parsedHook once its arguments have parsed
// so nothing else it does runs.
package main

import (
	"errors"
	"flag"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestSplitArgs_ReadsBoolFlagsFromTheFlagSet covers the splitter alone: a
// bool flag takes no following token, in its bare form or with an inline
// value, a value flag takes the next one, and positionals keep their order
// wherever the flags sit.
func TestSplitArgs_ReadsBoolFlagsFromTheFlagSet(t *testing.T) {
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.Bool("requires-elevation", false, "")
		fs.BoolFunc("trace", "", func(string) error { return nil })
		fs.String("engine-version", "", "")
		fs.String("tags", "", "")
		fs.String("dir", "", "")
		return fs
	}
	tests := []struct {
		name            string
		args            []string
		wantPositionals []string
		wantRest        []string
	}{
		{
			name:            "bool flag mid-args does not swallow the next flag",
			args:            []string{"name1", "--requires-elevation", "--engine-version", ">=1.0.0"},
			wantPositionals: []string{"name1"},
			wantRest:        []string{"--requires-elevation", "--engine-version", ">=1.0.0"},
		},
		{
			name:            "bool flag before the positional does not swallow it",
			args:            []string{"--requires-elevation", "name1"},
			wantPositionals: []string{"name1"},
			wantRest:        []string{"--requires-elevation"},
		},
		{
			name:            "a BoolFunc flag is a bool flag too",
			args:            []string{"-trace", "name1"},
			wantPositionals: []string{"name1"},
			wantRest:        []string{"-trace"},
		},
		{
			name:            "bool flag with an inline value takes no following token",
			args:            []string{"--requires-elevation=true", "name1"},
			wantPositionals: []string{"name1"},
			wantRest:        []string{"--requires-elevation=true"},
		},
		{
			name:            "positionals keep their order around value flags",
			args:            []string{"a.yaml", "--tags", "web", "b.yaml", "--dir=x", "c.yaml"},
			wantPositionals: []string{"a.yaml", "b.yaml", "c.yaml"},
			wantRest:        []string{"--tags", "web", "--dir=x"},
		},
		{
			name:            "a lone dash is a positional",
			args:            []string{"-", "--dir", "x"},
			wantPositionals: []string{"-"},
			wantRest:        []string{"--dir", "x"},
		},
		{
			name:            "-- ends the flags, and is dropped",
			args:            []string{"--dir", "x", "--", "-name1", "--tags", "web"},
			wantPositionals: []string{"-name1", "--tags", "web"},
			wantRest:        []string{"--dir", "x"},
		},
		{
			name:            "-- after a bool flag ends the flags too",
			args:            []string{"name1", "--requires-elevation", "--", "--dir"},
			wantPositionals: []string{"name1", "--dir"},
			wantRest:        []string{"--requires-elevation"},
		},
		{
			name:            "-- as a value flag's value is its value",
			args:            []string{"--tags", "--", "name1"},
			wantPositionals: []string{"name1"},
			wantRest:        []string{"--tags", "--"},
		},
		{
			name:     "flags alone leave no positional",
			args:     []string{"--dir", "x"},
			wantRest: []string{"--dir", "x"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			positionals, rest := splitArgs(newFS(), tt.args)
			if !reflect.DeepEqual(positionals, tt.wantPositionals) {
				t.Errorf("positionals = %q, want %q", positionals, tt.wantPositionals)
			}
			if !reflect.DeepEqual(rest, tt.wantRest) {
				t.Errorf("rest = %q, want %q", rest, tt.wantRest)
			}
		})
	}
}

// TestParseOneArg_CountsPositionals covers parseOneArg's two count errors
// and that a flag error is not one of them.
func TestParseOneArg_CountsPositionals(t *testing.T) {
	silenceStderr(t)
	fs := func() *flag.FlagSet { return flag.NewFlagSet("test", flag.ContinueOnError) }
	if _, err := parseOneArg(fs(), nil); !errors.Is(err, errMissingPositional) || !isPositionalCountErr(err) {
		t.Errorf("no positional = %v, want errMissingPositional", err)
	}
	if _, err := parseOneArg(fs(), []string{"a", "b"}); !errors.Is(err, errExtraPositional) || !strings.Contains(err.Error(), `"b"`) {
		t.Errorf("two positionals = %v, want errExtraPositional naming \"b\"", err)
	}
	if _, err := parseOneArg(fs(), []string{"--nope", "a"}); err == nil || isPositionalCountErr(err) {
		t.Errorf("an unknown flag = %v, want the flag error", err)
	}
}

// splitCommand is one command that splits its arguments with parseArgs.
type splitCommand struct {
	// name is how the command is typed.
	name string
	// call runs the command with args.
	call func(args []string) error
	// lead is what the command reads before it splits (journal's list).
	lead []string
	// positionals are positional arguments the command accepts.
	positionals []string
}

// splitCommands is every command whose arguments parseArgs splits.
// TestEverySplittingCommandIsInTheTable fails on a command that calls
// parseArgs, parseCheckedArgs or parseOneArg and is not here.
var splitCommands = []splitCommand{
	{name: "add-host", call: runAddHost, positionals: []string{"web1"}},
	{name: "set-host", call: runSetHost, positionals: []string{"web1"}},
	{name: "grant-capability", call: runGrantCapability, positionals: []string{"web1", "acme.thing"}},
	{name: "revoke-capability", call: runRevokeCapability, positionals: []string{"web1", "acme.thing"}},
	{name: "add-credential", call: runAddCredential, positionals: []string{"web1"}},
	{name: "add-registry-credential", call: runAddRegistryCredential, positionals: []string{"registry.example"}},
	{name: "add-database-connection", call: runAddDatabaseConnection, positionals: []string{"db1"}},
	{name: "remove-database-connection", call: runRemoveDatabaseConnection, positionals: []string{"db1"}},
	{name: "add-secret", call: runAddSecret, positionals: []string{"ike"}},
	{name: "remove-secret", call: runRemoveSecret, positionals: []string{"ike"}},
	{name: "remove-host", call: runRemoveHost, positionals: []string{"web1"}},
	{name: "remove-credential", call: runRemoveCredential, positionals: []string{"web1"}},
	{name: "import-kubeconfig", call: runImportKubeconfig, positionals: []string{"k1"}},
	{name: "onboard", call: runOnboard, positionals: []string{"web1"}},
	{name: "trust-host", call: runTrustHost, positionals: []string{"web1"}},
	{name: "validate", call: runValidate, positionals: []string{"a.yaml", "b.yaml"}},
	{name: "run", call: runRunbook, positionals: []string{"site.yaml"}},
	{name: "adhoc", call: runAdhoc, positionals: []string{"web1", "exec.command", "cmd=true"}},
	{name: "rollback", call: runRollback, positionals: []string{"run-1"}},
	{name: "facts", call: runFacts, positionals: []string{"web1", "os.family"}},
	{name: "journal list", call: runJournal, lead: []string{"list"}, positionals: []string{"run-1"}},
	{name: "doc", call: runDoc, positionals: []string{"exec.command"}},
	{name: "collection approve", call: runCollectionApprove, positionals: []string{"note"}},
	{name: "collection revoke", call: runCollectionRevoke, positionals: []string{"note"}},
	{name: "ai prompt sign", call: runAIPromptSign, positionals: []string{"bundle.json"}},
	{name: "ai review show", call: runAIReviewShow, positionals: []string{"0123456789abcdef"}},
	{name: "ai review approve", call: runAIReviewApprove, positionals: []string{"0123456789abcdef"}},
	{name: "ai review deny", call: runAIReviewDeny, positionals: []string{"0123456789abcdef"}},
	{name: "forge new-external", call: runForgeNewExternal, positionals: []string{"acme.thing"}},
	{name: "forge new-device", call: runForgeNewDevice, positionals: []string{"acme"}},
	{name: "forge new-collection", call: runForgeNewCollection, positionals: []string{"acme.thing"}},
	{name: "forge new-filter", call: runForgeNewFilter, positionals: []string{"CIDRToNetmask"}},
	{name: "forge new-view", call: runForgeNewView, positionals: []string{"widgets"}},
	{name: "forge new-plugin", call: runForgeNewPlugin, positionals: []string{"acme"}},
	{name: "forge migrate-playbook", call: runForgeMigratePlaybook, positionals: []string{"site.yml"}},
	{name: "import awx-credential-types", call: runImportAWXCredentialTypes, positionals: []string{"export.json"}},
}

// errStopAfterParse is what the hook returns to stop a command once its
// arguments have parsed.
var errStopAfterParse = errors.New("stopped after parsing")

// parsed is what one command's arguments parsed into.
type parsed struct {
	fs          *flag.FlagSet
	positionals []string
}

// parseWith runs c with args, stopped by parsedHook once they parse, and
// returns what they parsed into (nil when the command never reached the
// hook) and the command's error.
func parseWith(t *testing.T, c splitCommand, args []string) (*parsed, error) {
	t.Helper()
	var got *parsed
	parsedHook = func(fs *flag.FlagSet, positionals []string) error {
		got = &parsed{fs: fs, positionals: positionals}
		return errStopAfterParse
	}
	defer func() { parsedHook = nil }()
	err := c.call(append(slices.Clone(c.lead), args...))
	if got != nil && !errors.Is(err, errStopAfterParse) {
		t.Fatalf("%s %q reached the hook but returned %v", c.name, args, err)
	}
	return got, err
}

// silenceStderr sends the flag package's own messages to the null device
// for the rest of the test.
func silenceStderr(t *testing.T) {
	t.Helper()
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = devNull
	t.Cleanup(func() {
		os.Stderr = stderr
		_ = devNull.Close()
	})
}

// TestEveryCommandSplitsItsOwnBoolFlags drives every command in
// splitCommands through its real FlagSet. For each bool flag that FlagSet
// declares, the flag placed before or after the positionals leaves them
// all positional and is set true; "--" makes the token after it a positional
// though it starts with "-"; and an unknown flag before them is reported
// as the flag error it is, not as a missing or extra positional.
func TestEveryCommandSplitsItsOwnBoolFlags(t *testing.T) {
	silenceStderr(t)
	t.Chdir(t.TempDir())
	for _, c := range splitCommands {
		t.Run(c.name, func(t *testing.T) {
			base, err := parseWith(t, c, c.positionals)
			if base == nil {
				t.Fatalf("%s %q never parsed its arguments: %v", c.name, c.positionals, err)
			}
			if !reflect.DeepEqual(base.positionals, c.positionals) {
				t.Fatalf("%s %q split into positionals %q", c.name, c.positionals, base.positionals)
			}
			var bools []string
			base.fs.VisitAll(func(f *flag.Flag) {
				if boolValue(f.Value) {
					bools = append(bools, f.Name)
				}
			})
			for _, b := range bools {
				// Before the positionals, where a swallowed one goes
				// missing, and after them, where the flag package alone
				// would stop at the first positional.
				for _, args := range [][]string{
					append([]string{"--" + b}, c.positionals...),
					append([]string{"-" + b}, c.positionals...),
					append(slices.Clone(c.positionals), "--"+b),
				} {
					spelled := strings.Join(args, " ")
					got, err := parseWith(t, c, args)
					if got == nil {
						t.Errorf("%s %s did not parse: %v", c.name, spelled, err)
						continue
					}
					if !reflect.DeepEqual(got.positionals, c.positionals) {
						t.Errorf("%s %s: positionals %q, want %q (the flag swallowed one)", c.name, spelled, got.positionals, c.positionals)
					}
					if v := got.fs.Lookup(b).Value.String(); v != "true" {
						t.Errorf("%s %s: the flag is %q, want true", c.name, spelled, v)
					}
				}
			}

			// "--" ends the flags: the last positional, given after it
			// with a leading dash, is still a positional.
			last := len(c.positionals) - 1
			dashed := append(slices.Clone(c.positionals[:last]), "-"+c.positionals[last])
			ended := append(slices.Clone(c.positionals[:last]), "--", dashed[last])
			if got, err := parseWith(t, c, ended); got == nil {
				t.Errorf("%s %q did not parse: %v", c.name, ended, err)
			} else if !reflect.DeepEqual(got.positionals, dashed) {
				t.Errorf("%s %q: positionals %q, want %q", c.name, ended, got.positionals, dashed)
			}

			got, err := parseWith(t, c, append([]string{"-vxf"}, c.positionals...))
			if got != nil {
				t.Fatalf("%s -vxf parsed, into %q", c.name, got.positionals)
			}
			if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -vxf") {
				t.Errorf("%s -vxf %q = %v, want the flag error first", c.name, c.positionals, err)
			}
		})
	}
}
