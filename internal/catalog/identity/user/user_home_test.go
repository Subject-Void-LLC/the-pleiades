// Tests of the home directory a created account's undo removes: only one
// the create made, and only while it still holds what useradd put there.
package user_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/identity/user"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// copyTree copies src's files and directories into dst, as useradd -m
// copies the skeleton directory into a new home.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path) // #nosec G304 -- a path under the test's own directory
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644) // #nosec G306 -- a test fixture
	})
	if err != nil {
		t.Fatal(err)
	}
}

// inverseParams returns the undo a run recorded, failing when it recorded none.
func inverseParams(t *testing.T, h *harness) (map[string]any, string) {
	t.Helper()
	inv, ok := h.rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		t.Fatal("no undo recorded")
	}
	params, _ := inv["params"].(map[string]any)
	description, _ := inv["description"].(string)
	return params, description
}

func TestCreate_UndoRemovesOnlyAHomeItMade(t *testing.T) {
	for _, tc := range []struct {
		name string
		// home is the task's home parameter, relative to the harness
		// directory; empty means useradd's own default, dir/home/deploy.
		home string
		// before is made, relative to the harness directory, before the run.
		before      string
		createdHome string
		createHome  any
		wantFlag    bool
	}{
		{name: "default home, made by this run", createdHome: "home/deploy", wantFlag: true},
		{name: "named home, made by this run", home: "srv/app", createdHome: "srv/app", wantFlag: true},
		{name: "a home already there", before: "home/deploy", createdHome: "home/deploy"},
		{name: "create_home false", createdHome: "home/deploy", createHome: false},
		// The undo still names the home useradd was asked for; the remove
		// then keeps the account's real one (TestRemove_AHomeMovedSinceIsKept).
		{name: "the account got another home than useradd was asked for", createdHome: "elsewhere", wantFlag: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := absentUser
			state.createdHome = "@dir/" + tc.createdHome
			h := newHarness(t, state)
			if tc.before != "" {
				if err := os.MkdirAll(filepath.Join(h.dir, tc.before), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			extra := map[string]any{}
			if tc.home != "" {
				extra["home"] = filepath.Join(h.dir, tc.home)
			}
			if tc.createHome != nil {
				extra["create_home"] = tc.createHome
			}
			if _, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", extra)); err != nil {
				t.Fatalf("Create: %v", err)
			}
			params, description := inverseParams(t, h)
			flag, _ := params["remove_home_if_unchanged"].(bool)
			if flag != tc.wantFlag {
				t.Errorf("undo params %v: remove_home_if_unchanged = %v, want %v", params, flag, tc.wantFlag)
			}
			asked := filepath.Join(h.dir, "home", "deploy")
			if tc.home != "" {
				asked = filepath.Join(h.dir, tc.home)
			}
			if tc.wantFlag && (params["created_home"] != asked || !strings.Contains(description, asked)) {
				t.Errorf("undo %v, %q: want created_home and the description naming %s", params, description, asked)
			}
			if !tc.wantFlag && params["created_home"] != nil {
				t.Errorf("undo %v names a home it did not make", params)
			}
		})
	}
}

func TestCreate_DefaultsFallBackToUseraddsOwn(t *testing.T) {
	// A useradd -D that prints no HOME means useradd's own /home.
	state := absentUser
	state.defaultsRaw = "GROUP=100"
	state.createdHome = "/home/deploy-pleiades-test"
	h := newHarness(t, state)
	if _, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy-pleiades-test", nil)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if params, _ := inverseParams(t, h); params["remove_home_if_unchanged"] != true {
		t.Errorf("undo params %v, want the home under /home removed", params)
	}
}

func TestCreate_UnreadableDefaultsStopBeforeUseradd(t *testing.T) {
	state := absentUser
	state.defaultsExit = 1
	h := newHarness(t, state)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil || !strings.Contains(err.Error(), "useradd -D exited 1") {
		t.Errorf("err = %v, want useradd -D's failure", err)
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("useradd ran: %v", calls)
	}
}

func TestRemove_HomeIfUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name string
		// prepare makes the account's home, given the skeleton directory.
		prepare     func(t *testing.T, home, skel string)
		wantCall    string
		wantWarning string
	}{
		{"an untouched home goes with the account", func(t *testing.T, home, skel string) { copyTree(t, skel, home) }, "userdel -r deploy", ""},
		{"a file added since keeps it", func(t *testing.T, home, skel string) {
			copyTree(t, skel, home)
			if err := os.WriteFile(filepath.Join(home, "data.db"), []byte("app data"), 0o644); err != nil { // #nosec G306 -- a test fixture
				t.Fatal(err)
			}
		}, "userdel deploy", "kept"},
		{"a skeleton file changed since keeps it", func(t *testing.T, home, skel string) {
			copyTree(t, skel, home)
			if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("# mine now\n"), 0o644); err != nil { // #nosec G306 -- a test fixture
				t.Fatal(err)
			}
		}, "userdel deploy", "kept"},
		{"no home at all", func(*testing.T, string, string) {}, "userdel deploy", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := presentUser
			state.home = "@dir/srv/app"
			h := newHarness(t, state)
			home := filepath.Join(h.dir, "srv", "app")
			tc.prepare(t, home, filepath.Join(h.dir, "skel"))

			// The check predicts the same decision and changes nothing.
			if _, err := user.CheckRemove(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"remove_home_if_unchanged": true})); err != nil {
				t.Fatalf("CheckRemove: %v", err)
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Fatalf("the check removed something: %v", calls)
			}
			checkWarnings, _ := h.rc.stats[sdk.StatWarnings].([]string)

			if _, err := user.Remove(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"remove_home_if_unchanged": true})); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			if calls := h.invocations(t); len(calls) != 1 || calls[0] != tc.wantCall {
				t.Errorf("calls = %v, want %q", calls, tc.wantCall)
			}
			warnings, _ := h.rc.stats[sdk.StatWarnings].([]string)
			// The check's warning is conditional, since in a rollback the
			// undos before it have not run yet; the run's says what it did.
			for got, want := range map[*[]string]string{&checkWarnings: "would keep", &warnings: tc.wantWarning} {
				if tc.wantWarning == "" && len(*got) != 0 {
					t.Errorf("warnings = %v, want none", *got)
				}
				if tc.wantWarning != "" && (len(*got) != 1 || !strings.Contains((*got)[0], want) || !strings.Contains((*got)[0], home)) {
					t.Errorf("warnings = %v, want one saying %q about %s", *got, want, home)
				}
			}
		})
	}
}

// TestRemove_AHomeMovedSinceIsKept replays a create's undo after the
// account's home was moved (identity.user.modify home:): the home it now
// has is not the one the create made, so it is kept, however untouched,
// and the warning names both. The check's warning is conditional, since
// in a rollback the undo of the move runs first.
func TestRemove_AHomeMovedSinceIsKept(t *testing.T) {
	state := presentUser
	state.home = "@dir/srv/moved"
	h := newHarness(t, state)
	moved := filepath.Join(h.dir, "srv", "moved")
	copyTree(t, filepath.Join(h.dir, "skel"), moved)
	params := h.params("deploy", map[string]any{"remove_home_if_unchanged": true, "created_home": filepath.Join(h.dir, "srv", "app")})

	if _, err := user.CheckRemove(context.Background(), h.rc, h.device, params); err != nil {
		t.Fatalf("CheckRemove: %v", err)
	}
	if w, _ := h.rc.stats[sdk.StatWarnings].([]string); len(w) != 1 || !strings.Contains(w[0], "would keep the home") || !strings.Contains(w[0], moved) {
		t.Errorf("check warnings = %v, want one saying it would keep %s", w, moved)
	}
	if _, err := user.Remove(context.Background(), h.rc, h.device, params); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if calls := h.invocations(t); len(calls) != 1 || calls[0] != "userdel deploy" {
		t.Errorf("calls = %v, want userdel without -r", calls)
	}
	if w, _ := h.rc.stats[sdk.StatWarnings].([]string); len(w) != 1 || !strings.Contains(w[0], "kept the home") || !strings.Contains(w[0], moved) {
		t.Errorf("warnings = %v, want one saying it kept %s", w, moved)
	}
}

func TestRemove_HomeIfUnchangedRefusals(t *testing.T) {
	h := newHarness(t, presentUser)
	if _, err := user.Remove(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"created_home": "/srv/app"})); err == nil || !strings.Contains(err.Error(), "read only with") {
		t.Errorf("created_home alone: err = %v", err)
	}
	_, err := user.Remove(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"remove": true, "remove_home_if_unchanged": true}))
	if err == nil || !strings.Contains(err.Error(), "cannot both be set") {
		t.Errorf("both flags: err = %v", err)
	}
	if _, err := user.Remove(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"remove_home_if_unchanged": "yes"})); err == nil {
		t.Error("a non-boolean remove_home_if_unchanged was accepted")
	}

	state := presentUser
	state.defaultsExit = 1
	h = newHarness(t, state)
	if _, err := user.Remove(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"remove_home_if_unchanged": true})); err == nil || !strings.Contains(err.Error(), "useradd -D") {
		t.Errorf("unreadable defaults: err = %v", err)
	}

	// A skeleton directory that cannot be entered cannot be compared.
	if os.Geteuid() != 0 {
		state = presentUser
		state.home = "@dir/emptyhome"
		h = newHarness(t, state)
		if err := os.MkdirAll(filepath.Join(h.dir, "emptyhome"), 0o755); err != nil {
			t.Fatal(err)
		}
		skel := filepath.Join(h.dir, "skel")
		if err := os.Chmod(skel, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(skel, 0o755) })
		if _, err := user.Remove(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"remove_home_if_unchanged": true})); err == nil || !strings.Contains(err.Error(), "comparing") {
			t.Errorf("unreadable skeleton: err = %v", err)
		}
	}

	for _, tc := range []struct {
		name   string
		budget int
	}{
		{"the defaults cannot be read", 1},
		{"the home cannot be compared", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarnessBudgeted(t, presentUser, tc.budget)
			if _, err := user.Remove(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"remove_home_if_unchanged": true})); err == nil {
				t.Error("Remove succeeded with its session refused")
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Errorf("userdel ran: %v", calls)
			}
		})
	}

	for _, check := range []bool{false, true} {
		state := presentUser
		state.home = "@dir/used"
		h := newHarness(t, state)
		used := filepath.Join(h.dir, "used")
		if err := os.MkdirAll(used, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(used, "data"), []byte("x"), 0o644); err != nil { // #nosec G306 -- a test fixture
			t.Fatal(err)
		}
		h.rc.failOnKey = sdk.StatWarnings
		run := user.Remove
		if check {
			run = user.CheckRemove
		}
		if _, err := run(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"remove_home_if_unchanged": true})); err == nil || !strings.Contains(err.Error(), "injected failure") {
			t.Errorf("check %v, the warning cannot be recorded: err = %v", check, err)
		}
	}
}

// TestCreate_AReadThatFailsAfterUseraddKeepsItsUndo drops the connection
// after useradd has made the account: the task fails with its undo already
// recorded. A fresh create with a home takes five sessions (the account,
// useradd -D, the test for an existing home, useradd, the re-read).
func TestCreate_AReadThatFailsAfterUseraddKeepsItsUndo(t *testing.T) {
	h := newHarnessBudgeted(t, absentUser, 4)
	if _, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil)); err == nil {
		t.Fatal("Create succeeded with its re-read refused")
	}
	if calls := h.invocations(t); len(calls) != 1 || !strings.HasPrefix(calls[0], "useradd") {
		t.Fatalf("calls = %v, want the useradd that ran", calls)
	}
	if params, _ := inverseParams(t, h); params["name"] != "deploy" {
		t.Errorf("undo params %v, want the account it made", params)
	}
}
