// Tests that every pkg.apt method refuses a name apt-get or dpkg-query would
// read as something other than one package, before it connects, and that a
// dpkg-query failing to answer is a failure rather than "not installed"
// (FAILURE_PATTERNS 1174). Proving the refusal against a real apt-get is
// cmd/pleiades's TestCLI_AptRefusesANameItWouldReadAsAnOption.
package apt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/apt"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// aptMethods is every pkg.apt function a task can reach, real and check.
var aptMethods = map[string]func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error){
	"Install": apt.Install, "CheckInstall": apt.CheckInstall,
	"Remove": apt.Remove, "CheckRemove": apt.CheckRemove,
	"Upgrade": apt.Upgrade, "CheckUpgrade": apt.CheckUpgrade,
}

// TestMethods_RefuseANameAptWouldMisread: a name apt-get would read as an
// option, a pattern, or "remove the package before the dash" is refused by
// every method before anything is sent. The server accepts no session at
// all, so a method that got past its parameters would fail on the
// connection instead, with a different error.
func TestMethods_RefuseANameAptWouldMisread(t *testing.T) {
	for value, want := range map[string]string{
		"-oDPkg::Pre-Invoke::=touch /tmp/pwned": `name is refused: it begins with "-"`,
		"--autoremove":                          `name is refused: it begins with "-"`,
		"lib*":                                  "name is refused: it holds",
		"diffutils-":                            `name is refused: it ends in "-"`,
		"jq/bookworm":                           `name is refused: it holds "/"`,
		"jq=1.6":                                `name is refused: it holds "="`,
		"~nzsh-common":                          "name is refused: it does not begin with a letter or a digit",
		"^zsh$":                                 "name is refused: it does not begin with a letter or a digit",
	} {
		for label, method := range aptMethods {
			t.Run(label+" "+value, func(t *testing.T) {
				h := newHarnessBudgeted(t, absent, 0)
				_, err := method(context.Background(), h.rc, h.device, h.params(value, nil))
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want %q", err, want)
				}
				if calls := h.invocations(t); len(calls) != 0 {
					t.Errorf("apt-get ran: %v", calls)
				}
			})
		}
	}
}

// TestRemove_RefusesADependencyAptWouldMisread: each dependency is an
// operand of apt-get remove, held to the same rule as the name.
func TestRemove_RefusesADependencyAptWouldMisread(t *testing.T) {
	h := newHarnessBudgeted(t, installedCurrent, 0)
	_, err := apt.Remove(context.Background(), h.rc, h.device, h.params("jq", map[string]any{"dependencies": []any{"libjq1", "-f"}}))
	if err == nil || !strings.Contains(err.Error(), `dependencies[1] is refused: it begins with "-"`) {
		t.Fatalf("err = %v, want the dependency refused by its index", err)
	}
}

// TestQueryDpkg_AFailureToAnswerIsNotAbsent: dpkg-query exits 1 for a name
// it has no record of, and anything else is a failure to answer. Reading
// exit 2 (its usage error) as "not installed" is what sent a refused option
// on to apt-get install, so it now fails the task with nothing sent.
func TestQueryDpkg_AFailureToAnswerIsNotAbsent(t *testing.T) {
	state := absent
	state.dpkgExit = 2
	h := newHarness(t, state)
	_, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "dpkg-query -W curl exited 2") {
		t.Fatalf("err = %v, want dpkg-query's failure named", err)
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("apt-get ran after dpkg-query failed to answer: %v", calls)
	}
}

// TestPackageName_AcceptsRealNames: names APT really has, including one
// ending in "+", pass the rule and reach apt-get after a "--".
func TestPackageName_AcceptsRealNames(t *testing.T) {
	for _, name := range []string{"g++", "libstdc++6", "openssh-server", "libc6:amd64"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, absent)
			if _, err := apt.Install(context.Background(), h.rc, h.device, h.params(name, nil)); err != nil {
				t.Fatalf("Install(%s) = %v", name, err)
			}
			if calls := h.invocations(t); len(calls) != 1 || calls[0] != "install -y -- "+name {
				t.Errorf("apt-get calls = %v, want one install of %s after --", calls, name)
			}
		})
	}
}

// TestInstall_RefusesAVersionAptWouldMisread: the version is joined onto
// the name as name=version, so apt-get reads a pattern in it as many
// versions and a trailing "-" as "remove the package" (measured on Debian
// 12: "Remv diffutils"). Each is refused before anything is sent.
func TestInstall_RefusesAVersionAptWouldMisread(t *testing.T) {
	for value, want := range map[string]string{
		"*":        "version is refused: it holds",
		"1.[0-9]":  "version is refused: it holds",
		"1:3.8-4-": `version is refused: it ends in "-"`,
		"-1":       `version is refused: it begins with "-"`,
	} {
		for _, label := range []string{"Install", "CheckInstall"} {
			t.Run(label+" "+value, func(t *testing.T) {
				h := newHarnessBudgeted(t, installedCurrent, 0)
				_, err := aptMethods[label](context.Background(), h.rc, h.device, h.params("diffutils", map[string]any{"version": value}))
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want %q", err, want)
				}
				if calls := h.invocations(t); len(calls) != 0 {
					t.Errorf("apt-get ran: %v", calls)
				}
			})
		}
	}
}

// TestInstall_RefusesANameAptHasNoPackageFor: a fresh install asks APT for
// the candidate of exactly the name first, and refuses one it has none
// for: a virtual package, which apt-get installs a provider of under another
// name, and a word apt-cache answers with other packages' blocks because it
// read it as a regular expression (measured on Debian 12: "jq+" answered
// with libjs-jquery.sparkline's). Each would have installed packages
// dpkg-query never reports under the word, so the undo recorded by the word
// would remove nothing. pkg.apt.upgrade installs an absent package the same
// way and refuses the same names.
func TestInstall_RefusesANameAptHasNoPackageFor(t *testing.T) {
	regex := pkgState{candidate: "2.1.2-3", policyHeader: "libjs-jquery.sparkline"}
	for _, tc := range []struct {
		label, name string
		state       pkgState
		method      func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error)
	}{
		{"a virtual package", "awk", noCandidate, apt.Install},
		{"a word read as a regular expression", "jq+", regex, apt.Install},
		{"upgrade, a virtual package", "awk", noCandidate, apt.Upgrade},
		{"upgrade, a regular expression", "jq+", regex, apt.Upgrade},
		{"the check of a regular expression", "jq+", regex, apt.CheckInstall},
	} {
		t.Run(tc.label, func(t *testing.T) {
			h := newHarness(t, tc.state)
			_, err := tc.method(context.Background(), h.rc, h.device, h.params(tc.name, nil))
			if err == nil || !strings.Contains(err.Error(), "no") || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("err = %v, want the name refused for having no package of its own", err)
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Errorf("apt-get ran: %v", calls)
			}
		})
	}
}

// TestQueryCandidate_ReadsTheSameWordsUnderAnyLocale: the fake apt-cache
// writes "Candidat :" unless LC_ALL is C, as the real one does under a
// French locale (measured on Debian 12, FAILURE_PATTERNS 1175). The check
// reads the candidate all the same, because the query sets LC_ALL=C.
func TestQueryCandidate_ReadsTheSameWordsUnderAnyLocale(t *testing.T) {
	h := newHarness(t, installedOld)
	if _, err := apt.CheckUpgrade(context.Background(), h.rc, h.device, h.params("curl", nil)); err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	diff, _ := h.rc.stats[sdk.StatDiff].(map[string]any)
	if after, _ := diff["after"].(map[string]any); after["version"] != "1.1" {
		t.Fatalf("predicted %v, want the candidate 1.1 read from apt-cache", after)
	}
}
