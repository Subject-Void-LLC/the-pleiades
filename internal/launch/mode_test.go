// Package launch_test: tests of the mode field and its resolution.
package launch_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// modeTemplate is a runbook template, with the given defaults, that opens
// no field at all to a launch: whatever a test shows about the mode, it
// shows without the template's permission.
func modeTemplate(kind string, defaults launch.Fields) launch.Template {
	return launch.Template{
		Name: "mode", KindName: kind, Definition: "patch-edge", InventoryID: 7, OrganizationID: 3,
		Defaults: defaults,
	}
}

// TestResolveMode covers the narrowing rule across the template's
// defaults, a saved configuration and a launch: a check at any layer wins
// and needs no permission, a real run beneath a check is refused, a value
// that is not a mode is refused wherever it arrives, and a kind with no
// mode refuses a check.
func TestResolveMode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     string
		defaults launch.Fields
		saved    launch.Fields
		launch   launch.Fields
		want     collection.Mode
		refused  string
	}{
		{name: "nothing asks", kind: "runbook", want: collection.ModeExecute},
		{name: "a launch asks for a check on a template that opens nothing", kind: "runbook", launch: launch.Fields{"mode": "check"}, want: collection.ModeCheck},
		{name: "a schedule asks for a check", kind: "runbook", saved: launch.Fields{"mode": "check"}, want: collection.ModeCheck},
		{name: "the template checks, the launch agrees", kind: "runbook", defaults: launch.Fields{"mode": "check"}, launch: launch.Fields{"mode": "check"}, want: collection.ModeCheck},
		{name: "an explicit real run narrowed by the launch", kind: "runbook", defaults: launch.Fields{"mode": "execute"}, launch: launch.Fields{"mode": "check"}, want: collection.ModeCheck},
		{name: "a launch cannot undo the template's check", kind: "runbook", defaults: launch.Fields{"mode": "check"}, launch: launch.Fields{"mode": "execute"}, refused: "never turned back into a real run"},
		{name: "a launch cannot undo a schedule's check", kind: "runbook", saved: launch.Fields{"mode": "check"}, launch: launch.Fields{"mode": "execute"}, refused: "never turned back into a real run"},
		{name: "a schedule cannot undo the template's check", kind: "runbook", defaults: launch.Fields{"mode": "check"}, saved: launch.Fields{"mode": "execute"}, refused: "never turned back into a real run"},
		{name: "a misspelled check", kind: "runbook", launch: launch.Fields{"mode": "chekc"}, refused: `a mode is "execute" or "check"`},
		{name: "a mode that is not text", kind: "runbook", launch: launch.Fields{"mode": true}, refused: `a mode is "execute" or "check"`},
		{name: "an empty mode", kind: "runbook", saved: launch.Fields{"mode": ""}, refused: `a mode is "execute" or "check"`},
		{name: "a playbook cannot be checked", kind: "playbook", launch: launch.Fields{"mode": "check"}, refused: "cannot be a check"},
		{name: "a playbook run asked for as a real run", kind: "playbook", launch: launch.Fields{"mode": "execute"}, want: collection.ModeExecute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl := modeTemplate(tc.kind, tc.defaults)
			resolved, ignored, err := tmpl.Resolve(context.Background(), launch.Config{Saved: tc.saved, Overrides: tc.launch})
			if tc.refused != "" {
				if !errors.Is(err, launch.ErrMode) || !strings.Contains(err.Error(), tc.refused) {
					t.Fatalf("Resolve = %v, want ErrMode containing %q", err, tc.refused)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if resolved.Mode != tc.want {
				t.Errorf("Mode = %q, want %q", resolved.Mode, tc.want)
			}
			for _, i := range ignored {
				if i.Name == launch.ModeField {
					t.Errorf("the mode was reported as ignored (%+v); it is either applied or refused", i)
				}
			}
			recorded, has := resolved.Fields[launch.ModeField]
			switch {
			case tc.kind == "runbook" && recorded != string(tc.want):
				t.Errorf("the job records mode %v, want %q", recorded, tc.want)
			case tc.kind == "playbook" && has:
				t.Errorf("a playbook job records a mode (%v)", recorded)
			}
		})
	}
}

// TestModeIsRefusedWhenSaved covers the save-time half: a template default
// that is not a mode fails Validate, and a saved configuration that could
// never launch fails CheckSavedMode, rather than failing at every firing.
func TestModeIsRefusedWhenSaved(t *testing.T) {
	bad := modeTemplate("runbook", launch.Fields{"mode": "chekc"})
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "must be one of execute, check") {
		t.Errorf("Validate of a template defaulting to mode chekc = %v, want it refused", err)
	}
	checking := modeTemplate("runbook", launch.Fields{"mode": "check"})
	if err := checking.Validate(); err != nil {
		t.Fatalf("a checking template does not validate: %v", err)
	}
	for saved, ok := range map[string]bool{"check": true, "execute": false, "sometimes": false} {
		err := checking.CheckSavedMode(launch.Fields{"mode": saved})
		if (err == nil) != ok || (err != nil && !errors.Is(err, launch.ErrMode)) {
			t.Errorf("CheckSavedMode(%q) on a checking template = %v, want ok=%v", saved, err, ok)
		}
	}
	if err := modeTemplate("playbook", nil).CheckSavedMode(launch.Fields{"mode": "check"}); !errors.Is(err, launch.ErrMode) {
		t.Errorf("a playbook schedule saved as a check = %v, want it refused", err)
	}
}

// TestChoiceField covers the field type the mode is declared with: only a
// listed value normalizes, and a kind cannot declare a choice field with no
// choices or a repeated one.
func TestChoiceField(t *testing.T) {
	d, ok := launch.Lookup("runbook")
	if !ok {
		t.Fatal("the runbook kind is not registered")
	}
	for value, ok := range map[any]bool{"execute": true, "check": true, "Check": false, "": false, 1: false} {
		if _, err := d.Normalize(launch.ModeField, value); (err == nil) != ok {
			t.Errorf("Normalize(mode, %v) = %v, want ok=%v", value, err, ok)
		}
	}
	defer launch.SnapshotForTest()()
	for name, choices := range map[string][]string{"none": nil, "repeated": {"a", "a"}, "empty": {"a", ""}} {
		err := launch.Register(launch.Descriptor{
			Kind: "choicetest-" + name, Label: "Choice test", Adapter: "native",
			Fields: []launch.FieldSpec{{Name: "pick", Type: launch.TypeChoice, Choices: choices}},
		})
		if err == nil {
			t.Errorf("a choice field with %s choices was registered", name)
		}
	}
}
