// Tests of the dependencies an install pulls in and its undo removes:
// recorded by the install, and removed with the package only while
// nothing else needs them and removing them takes nothing else along.
package apt_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/apt"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func TestInstall_UndoNamesWhatTheInstallPulledIn(t *testing.T) {
	h := newHarness(t, pkgState{listBefore: "base-files", listAfter: "base-files jq libonig5 libjq1"})
	if _, err := apt.Install(context.Background(), h.rc, h.device, h.params("jq", nil)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if calls := h.invocations(t); len(calls) != 1 || calls[0] != "install -y jq" {
		t.Errorf("apt-get calls = %v, want one install", calls)
	}
	inv, _ := h.rc.stats[sdk.StatInverse].(map[string]any)
	params, _ := inv["params"].(map[string]any)
	if got, _ := params["dependencies"].([]any); !reflect.DeepEqual(got, []any{"libjq1", "libonig5"}) {
		t.Errorf("undo dependencies = %v, want [libjq1 libonig5] (jq itself is the name)", params["dependencies"])
	}
	if d, _ := inv["description"].(string); !strings.Contains(d, "libjq1, libonig5") {
		t.Errorf("undo description %q does not name what it removes", d)
	}
}

func TestInstall_AnUnreadableInstalledSetStopsBeforeInstalling(t *testing.T) {
	h := newHarness(t, pkgState{listExit: 2})
	_, err := apt.Install(context.Background(), h.rc, h.device, h.params("jq", nil))
	if err == nil || !strings.Contains(err.Error(), "dpkg-query -W exited 2") {
		t.Errorf("err = %v, want the listing's failure", err)
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("apt-get ran: %v", calls)
	}
}

func TestRemove_TakesItsDependenciesWhileNothingNeedsThem(t *testing.T) {
	jq := installedCurrent
	jq.listBefore = "jq libjq1 libonig5 other"
	for _, tc := range []struct {
		name               string
		simAuto, simPlain  string
		deps               []any
		wantRemove         string
		wantRemoved        []string
		wantKeptInWarnings string
	}{
		{name: "both unneeded", simAuto: "jq libjq1 libonig5:amd64", simPlain: "jq libjq1 libonig5",
			deps: []any{"libonig5", "libjq1", "libjq1", "gone"}, wantRemove: "remove -y jq libjq1 libonig5", wantRemoved: []string{"libjq1", "libonig5"}},
		{name: "one still needed", simAuto: "jq libjq1", simPlain: "jq libjq1",
			deps: []any{"libjq1", "libonig5"}, wantRemove: "remove -y jq libjq1", wantRemoved: []string{"libjq1"}, wantKeptInWarnings: "kept libonig5:"},
		{name: "removing them would take another package", simAuto: "jq libjq1 libonig5", simPlain: "jq libjq1 libonig5 other",
			deps: []any{"libjq1", "libonig5"}, wantRemove: "remove -y jq", wantKeptInWarnings: "kept libjq1, libonig5:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := jq
			state.simAuto, state.simPlain = tc.simAuto, tc.simPlain
			params := map[string]any{"dependencies": tc.deps}

			// The check predicts the same and removes nothing.
			check := newHarness(t, state)
			if _, err := apt.CheckRemove(context.Background(), check.rc, check.device, check.params("jq", params)); err != nil {
				t.Fatalf("CheckRemove: %v", err)
			}
			for _, call := range check.invocations(t) {
				if !strings.HasPrefix(call, "-s ") {
					t.Errorf("the check sent %q", call)
				}
			}

			h := newHarness(t, state)
			if _, err := apt.Remove(context.Background(), h.rc, h.device, h.params("jq", params)); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			calls := h.invocations(t)
			if len(calls) == 0 || calls[len(calls)-1] != tc.wantRemove {
				t.Errorf("apt-get calls = %v, want the last %q", calls, tc.wantRemove)
			}
			for _, rc := range []*ctxStub{check.rc, h.rc} {
				removed, _ := rc.stats["dependencies_removed"].([]string)
				if len(removed) != len(tc.wantRemoved) || (len(removed) > 0 && !reflect.DeepEqual(removed, tc.wantRemoved)) {
					t.Errorf("dependencies_removed = %v, want %v", removed, tc.wantRemoved)
				}
				warnings, _ := rc.stats[sdk.StatWarnings].([]string)
				if tc.wantKeptInWarnings == "" && len(warnings) != 0 {
					t.Errorf("warnings = %v, want none", warnings)
				}
				if tc.wantKeptInWarnings != "" && (len(warnings) != 1 || !strings.Contains(warnings[0], tc.wantKeptInWarnings)) {
					t.Errorf("warnings = %v, want one starting %q", warnings, tc.wantKeptInWarnings)
				}
			}
		})
	}
}

func TestRemove_AnAbsentPackageLeavesItsDependencies(t *testing.T) {
	h := newHarness(t, absent)
	if _, err := apt.Remove(context.Background(), h.rc, h.device, h.params("jq", map[string]any{"dependencies": []any{"libjq1"}})); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("apt-get calls = %v, want none", calls)
	}
	if _, ok := h.rc.stats["dependencies_removed"]; ok {
		t.Error("an absent package recorded dependencies removed")
	}
}

func TestRemove_DependencyFailures(t *testing.T) {
	deps := map[string]any{"dependencies": []any{"libjq1"}}
	base := installedCurrent
	base.listBefore, base.simAuto, base.simPlain = "jq libjq1", "jq libjq1", "jq libjq1"

	h := newHarness(t, base)
	if _, err := apt.Remove(context.Background(), h.rc, h.device, h.params("jq", map[string]any{"dependencies": "libjq1"})); err == nil {
		t.Error("a dependencies value that is not a list was accepted")
	}

	for _, tc := range []struct {
		name   string
		change func(*pkgState)
		want   string
	}{
		{"the installed set cannot be read", func(s *pkgState) { s.listExit = 2 }, "dpkg-query -W exited 2"},
		{"the autoremove simulation fails", func(s *pkgState) { s.simExit = 100 }, "exited 100"},
		{"the removal simulation fails", func(s *pkgState) { s.simPlainExit = 100 }, "exited 100"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := base
			tc.change(&state)
			h := newHarness(t, state)
			_, err := apt.Remove(context.Background(), h.rc, h.device, h.params("jq", deps))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one naming %q", err, tc.want)
			}
			for _, call := range h.invocations(t) {
				if strings.HasPrefix(call, "remove ") {
					t.Errorf("the package was removed anyway: %q", call)
				}
			}
		})
	}

	// A remove with dependencies takes six sessions: the query, the
	// installed set, the two simulations, the remove and the re-query.
	for budget, what := range map[int]string{1: "the installed set", 2: "the autoremove simulation", 3: "the removal simulation"} {
		h := newHarnessBudgeted(t, base, budget)
		if _, err := apt.Remove(context.Background(), h.rc, h.device, h.params("jq", deps)); err == nil || !strings.Contains(err.Error(), "open session") {
			t.Errorf("%s: err = %v, want the session failing to open", what, err)
		}
	}

	for _, tc := range []struct {
		key   string
		state func() pkgState
	}{
		{"dependencies_removed", func() pkgState { return base }},
		{sdk.StatWarnings, func() pkgState { s := base; s.simAuto = "jq"; return s }},
	} {
		for _, check := range []bool{false, true} {
			h := newHarness(t, tc.state())
			h.rc.failOnKey = tc.key
			run := apt.Remove
			if check {
				run = apt.CheckRemove
			}
			if _, err := run(context.Background(), h.rc, h.device, h.params("jq", deps)); err == nil || !strings.Contains(err.Error(), "injected failure") {
				t.Errorf("check %v, %s cannot be recorded: err = %v", check, tc.key, err)
			}
		}
	}
}

// TestInstall_AReadThatFailsAfterTheInstallKeepsItsUndo drops the
// connection after apt-get has installed the package: the task fails, but
// the undo is already recorded, so the change it made can still be rolled
// back. A fresh install takes five sessions (the query, the installed set
// before, the install, the set after, the re-query); budget is how many
// succeed.
func TestInstall_AReadThatFailsAfterTheInstallKeepsItsUndo(t *testing.T) {
	for budget, what := range map[int]string{3: "the set after", 4: "the re-query"} {
		h := newHarnessBudgeted(t, absent, budget)
		_, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
		if err == nil || !strings.Contains(err.Error(), "open session") {
			t.Errorf("%s failing: err = %v, want the session failure", what, err)
		}
		inv, _ := h.rc.stats[sdk.StatInverse].(map[string]any)
		params, _ := inv["params"].(map[string]any)
		if inv["fqcn"] != "pkg.apt.remove" || params["name"] != "curl" {
			t.Errorf("%s failing: undo = %v, want pkg.apt.remove naming curl", what, inv)
		}
		if budget != 3 {
			continue
		}
		if _, listed := params["dependencies"]; listed {
			t.Errorf("an undo named dependencies nothing read: %v", params)
		}
		warnings, _ := h.rc.stats[sdk.StatWarnings].([]string)
		if len(warnings) != 1 || !strings.Contains(warnings[0], "could not read what installing curl pulled in") {
			t.Errorf("warnings = %v, want one saying the dependencies could not be read", warnings)
		}
	}
}

// TestInstall_AnUndoNamesNoMoreDependenciesThanTheJournalHolds installs a
// package pulling in as many dependencies as an undo may name, then one
// more. The first undo names them all and the journal accepts the list;
// the second names the package alone with a warning, because the journal
// refuses a longer list and would leave the whole run's undo incomplete.
// The journal's own check is the control, so the two limits cannot drift.
func TestInstall_AnUndoNamesNoMoreDependenciesThanTheJournalHolds(t *testing.T) {
	names := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("lib%03d", i)
		}
		return out
	}
	for _, n := range []int{128, 129} {
		h := newHarness(t, pkgState{listBefore: "base-files", listAfter: "base-files big " + strings.Join(names(n), " ")})
		if _, err := apt.Install(context.Background(), h.rc, h.device, h.params("big", nil)); err != nil {
			t.Fatalf("%d dependencies: Install: %v", n, err)
		}
		inv, _ := h.rc.stats[sdk.StatInverse].(map[string]any)
		params, _ := inv["params"].(map[string]any)
		deps, listed := params["dependencies"]
		_, journalHolds := engine.RecordableTexts(names(n))
		if listed != journalHolds {
			t.Errorf("%d dependencies: the undo lists them %v, the journal holds such a list %v", n, listed, journalHolds)
		}
		if listed {
			if _, ok := engine.RecordableTexts(deps); !ok {
				t.Errorf("%d dependencies: the journal refuses the list the undo named", n)
			}
			continue
		}
		warnings, _ := h.rc.stats[sdk.StatWarnings].([]string)
		if len(warnings) != 1 || !strings.Contains(warnings[0], fmt.Sprintf("pulled in %d packages", n)) {
			t.Errorf("%d dependencies: warnings = %v, want one saying the undo removes the package alone", n, warnings)
		}
	}
}
