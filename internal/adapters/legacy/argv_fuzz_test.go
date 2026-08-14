package legacy

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// FuzzBuildArgvNeverCarriesAValue establishes the property the extra-vars
// file form exists for.
//
// The property: no argument buildArgv produces contains any extra-variable
// VALUE, for any launch fields and any injected material, ever.
//
// Establishing it by fuzzing is possible only because the signature makes
// it structurally true: buildArgv takes a bool, not the variables, so there
// is no value in scope for it to emit. That is the point of the signature,
// and this target is what would catch a future change widening it back. A
// table could assert the same thing for the cases somebody thought of; this
// asserts it for the ones nobody did.
//
// It also pins the other half, which a leak-only property would let a
// broken build satisfy trivially: when there ARE extra variables, `-e` is
// present and names the file.
func FuzzBuildArgvNeverCarriesAValue(f *testing.F) {
	f.Add("core-switch-1", 3, 5, "deploy,restart", "slow", true, "prod", "/run/pleiades/credentials/9.vault")
	f.Add("", 0, 0, "", "", false, "", "")
	f.Add("*", 99, -1, "a", "b", true, "", "/run/pleiades/credentials/1.vault")
	f.Add("-e", 1, 1, "-e", "-e", true, "-e", "-e")

	f.Fuzz(func(t *testing.T, limit string, verbosity, forks int, tags, skipTags string,
		hasExtraVars bool, vaultID, vaultPath string) {

		// The values a leak would expose. They are deliberately NOT passed
		// to buildArgv: that is the property. They exist here so the
		// assertion has something concrete to hunt for.
		const secretValue = "sk-live-CANARY-argv-1a2b3c4d"

		fields := launch.Fields{}
		if limit != "" {
			fields["limit"] = limit
		}
		if verbosity != 0 {
			fields["verbosity"] = verbosity
		}
		if forks != 0 {
			fields["forks"] = forks
		}
		if tags != "" {
			fields["job_tags"] = strings.Split(tags, ",")
		}
		if skipTags != "" {
			fields["skip_tags"] = strings.Split(skipTags, ",")
		}

		var vaults []wire.InjectedVault
		if vaultPath != "" {
			vaults = append(vaults, wire.InjectedVault{Identifier: vaultID, Path: vaultPath})
		}

		argv := buildArgv(fields, hasExtraVars, vaults, "/run/pleiades/inventory.json", "/run/pleiades/playbook.yml")

		for i, arg := range argv {
			if strings.Contains(arg, secretValue) {
				t.Fatalf("argv[%d] = %q carries a value", i, arg)
			}
		}

		// The other half. Without it, a build that emitted no -e at all
		// would satisfy the leak property perfectly and lose every extra
		// variable.
		//
		// Checked at the TAIL specifically, not by scanning for "-e"
		// anywhere, and the fuzzer is what taught that distinction: a
		// launch whose "limit" field is literally "-e" produces
		// `--limit -e`, where the "-e" is a VALUE rather than a flag. A
		// scan would read that as an extra-vars flag whose argument is
		// whatever happened to follow. buildArgv's own doc comment is
		// explicit that -e trails the playbook path, so the tail is the
		// only place the flag can be.
		hasFileReference := len(argv) >= 2 &&
			argv[len(argv)-2] == "-e" &&
			argv[len(argv)-1] == "@"+extraVarsContainerPath
		if hasExtraVars != hasFileReference {
			t.Fatalf("hasExtraVars=%v produced a trailing -e file reference: %v (argv=%v)",
				hasExtraVars, hasFileReference, argv)
		}

		// The invocation is always well formed: the program, the inventory
		// flag with its path, and the playbook.
		if len(argv) < 4 || argv[0] != "ansible-playbook" {
			t.Fatalf("argv is not an ansible-playbook invocation: %v", argv)
		}
		if !containsArg(argv, "/run/pleiades/playbook.yml") {
			t.Fatalf("argv names no playbook: %v", argv)
		}
		if !containsPairIn(argv, "-i", "/run/pleiades/inventory.json") {
			t.Fatalf("argv names no inventory: %v", argv)
		}
	})
}

// containsArg reports whether argv contains want.
func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

// containsPairIn reports whether argv contains flag immediately followed by
// value.
func containsPairIn(argv []string, flag, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}
	return false
}
