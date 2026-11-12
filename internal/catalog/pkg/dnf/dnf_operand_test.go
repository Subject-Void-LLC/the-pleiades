// Tests that every pkg.dnf method refuses a name dnf or rpm would read as
// something other than one package, before it connects, sends dnf no "--"
// (dnf5 refuses one), refuses a name no package in the repositories has,
// and treats rpm failing to answer as a failure rather than "not installed",
// whatever the device's locale (FAILURE_PATTERNS 1174 and 1175).
package dnf_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/dnf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// dnfMethods is every pkg.dnf function a task can reach, real and check.
var dnfMethods = map[string]func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error){
	"Install": dnf.Install, "CheckInstall": dnf.CheckInstall,
	"Remove": dnf.Remove, "CheckRemove": dnf.CheckRemove,
	"Upgrade": dnf.Upgrade, "CheckUpgrade": dnf.CheckUpgrade,
}

// TestMethods_RefuseANameDnfWouldMisread: a name dnf would read as an
// option or as a pattern (dnf install "*" installs every package it can
// find) is refused by every method before anything is sent. The server
// accepts no session, so a method that got past its parameters would fail
// on the connection instead, with a different error.
func TestMethods_RefuseANameDnfWouldMisread(t *testing.T) {
	for value, want := range map[string]string{
		"--setopt=tsflags=noscripts": `name is refused: it begins with "-"`,
		"-x":                         `name is refused: it begins with "-"`,
		"*":                          "name is refused: it holds",
		"openssh*":                   "name is refused: it holds",
		"@core":                      `name is refused: it begins with "@"`,
		"/usr/bin/jq":                `name is refused: it holds "/"`,
		"perl(strict)":               `name is refused: it holds "(" or ")"`,
		"nginx >= 1.20":              "name is refused: it holds a space or a comparison",
	} {
		for label, method := range dnfMethods {
			t.Run(label+" "+value, func(t *testing.T) {
				h := newHarnessBudgeted(t, absent, 0)
				_, err := method(context.Background(), h.rc, h.device, h.params(value, nil))
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want %q", err, want)
				}
				if calls := h.invocations(t); len(calls) != 0 {
					t.Errorf("dnf ran: %v", calls)
				}
			})
		}
	}
}

// TestMethods_SendTheNameWithNoEndOfOptions: each command that changes a
// package carries its name right after the command's own options, with no
// "--": dnf5 refuses one for install, remove, upgrade and check-update and
// exits 2 (measured on Fedora 42), and the fake dnf here does the same, so a
// "--" fails each case. The name rule is what keeps it from the options.
func TestMethods_SendTheNameWithNoEndOfOptions(t *testing.T) {
	for _, tc := range []struct {
		label  string
		state  pkgState
		method func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error)
		want   string
	}{
		{"install", absent, dnf.Install, "install -y curl"},
		{"remove", installedCurrent, dnf.Remove, "remove -y curl"},
		{"upgrade, absent", absent, dnf.Upgrade, "install -y curl"},
		{"upgrade, old", installedOld, dnf.Upgrade, "upgrade -y curl"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			h := newHarness(t, tc.state)
			if _, err := tc.method(context.Background(), h.rc, h.device, h.params("curl", nil)); err != nil {
				t.Fatalf("%s: %v", tc.label, err)
			}
			if calls := h.invocations(t); len(calls) != 1 || calls[0] != tc.want {
				t.Errorf("dnf calls = %v, want [%s]", calls, tc.want)
			}
		})
	}
}

// TestQueryRPM_AFailureToAnswerIsNotAbsent: rpm -q exits 1 both for an
// absent package and for an option or a database it cannot read, so only
// its own "is not installed" reads as absent. Any other failure stops the
// task with nothing sent, naming what rpm said.
func TestQueryRPM_AFailureToAnswerIsNotAbsent(t *testing.T) {
	h := newHarness(t, pkgState{rpmError: "error: cannot open Packages database in /var/lib/rpm"})
	_, err := dnf.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "rpm -q curl exited 1: error: cannot open Packages database") {
		t.Fatalf("err = %v, want rpm's failure named", err)
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("dnf ran after rpm failed to answer: %v", calls)
	}
}

// TestInstall_RefusesAVersionDnfWouldMisread: the version is joined onto
// the name as name-version, so a pattern in it is a pattern over packages
// (dnf install "nginx-*" installs every nginx- package). Each is refused
// before anything is sent.
func TestInstall_RefusesAVersionDnfWouldMisread(t *testing.T) {
	for value, want := range map[string]string{
		"*":        "version is refused: it holds",
		"1.2?":     "version is refused: it holds",
		"1.0-":     `version is refused: it ends in "-"`,
		"1.0 bash": "version is refused: it holds a character",
	} {
		for _, label := range []string{"Install", "CheckInstall"} {
			t.Run(label+" "+value, func(t *testing.T) {
				h := newHarnessBudgeted(t, installedCurrent, 0)
				_, err := dnfMethods[label](context.Background(), h.rc, h.device, h.params("nginx", map[string]any{"version": value}))
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want %q", err, want)
				}
				if calls := h.invocations(t); len(calls) != 0 {
					t.Errorf("dnf ran: %v", calls)
				}
			})
		}
	}
}

// TestInstall_RefusesANameNoPackageHas: dnf install also reads a word as a
// capability another package provides (on Rocky Linux 9, dnf install vim
// installs vim-enhanced and rpm -q vim still reports vim absent), so a
// fresh install first asks dnf repoquery, which lists only packages by name,
// and refuses a word it lists none for. pkg.dnf.upgrade installs an absent
// package the same way and refuses the same name.
func TestInstall_RefusesANameNoPackageHas(t *testing.T) {
	capability := pkgState{repoquery: "none"}
	for label, method := range map[string]func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error){
		"install": dnf.Install, "upgrade": dnf.Upgrade,
	} {
		t.Run(label, func(t *testing.T) {
			h := newHarness(t, capability)
			_, err := method(context.Background(), h.rc, h.device, h.params("vim", nil))
			if err == nil || !strings.Contains(err.Error(), "no package in this device's repositories is named vim") {
				t.Fatalf("err = %v, want vim refused as no package's name", err)
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Errorf("dnf changed something: %v", calls)
			}
		})
	}
}

// TestQueryRPM_ReadsAbsenceUnderAnyLocale: the fake rpm says a package is
// absent in French unless LC_ALL is C, as the real one does (measured on
// Fedora 42: "le paquet nosuchpkg n'est pas install" and an accented e). queryRPM sets
// LC_ALL=C, so an absent package still reads as absent and is installed;
// without it the install failed on every French-speaking host
// (FAILURE_PATTERNS 1175).
func TestQueryRPM_ReadsAbsenceUnderAnyLocale(t *testing.T) {
	h := newHarness(t, absent)
	result, err := dnf.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil || !result.Changed {
		t.Fatalf("Install = changed %v, %v; want the absent package installed", result.Changed, err)
	}
}

// TestInstall_ARepoqueryThatCannotAnswerStopsTheInstall: dnf repoquery
// failing to answer is not "no such package" and not "a package": the task
// fails naming it, with nothing installed.
func TestInstall_ARepoqueryThatCannotAnswerStopsTheInstall(t *testing.T) {
	h := newHarness(t, pkgState{repoqueryExit: 1})
	_, err := dnf.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "dnf repoquery curl exited 1: Error: fake repoquery failure") {
		t.Fatalf("err = %v, want repoquery's failure named", err)
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("dnf changed something: %v", calls)
	}
}
