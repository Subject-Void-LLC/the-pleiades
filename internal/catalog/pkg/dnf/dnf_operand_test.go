// Tests that every pkg.dnf method refuses a name dnf or rpm would read as
// something other than one package, before it connects, sends every name it
// does pass after a "--", and treats rpm failing to answer as a failure
// rather than "not installed" (FAILURE_PATTERNS 1174).
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

// TestMethods_SendTheNameAfterTheEndOfOptions: each command that changes a
// package carries its name after a "--".
func TestMethods_SendTheNameAfterTheEndOfOptions(t *testing.T) {
	for _, tc := range []struct {
		label  string
		state  pkgState
		method func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error)
		want   string
	}{
		{"install", absent, dnf.Install, "install -y -- curl"},
		{"remove", installedCurrent, dnf.Remove, "remove -y -- curl"},
		{"upgrade, absent", absent, dnf.Upgrade, "install -y -- curl"},
		{"upgrade, old", installedOld, dnf.Upgrade, "upgrade -y -- curl"},
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
