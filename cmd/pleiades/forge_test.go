package main

import (
	"errors"
	"testing"
)

// TestRunForge_NoArgs mirrors run()'s own empty-args behavior: no
// subcommand named means the same unknown-command failure shape, not a
// silent success, since forge is a namespace rather than a runnable
// default action.
func TestRunForge_NoArgs(t *testing.T) {
	err := runForge(nil)
	if !errors.Is(err, errUnknownCommand) {
		t.Errorf("runForge(nil) = %v, want errUnknownCommand", err)
	}
}

// TestRunForge_Help confirms all three help spellings main.go's own run()
// recognizes are also recognized here, and succeed rather than falling
// through to the unknown-command path.
func TestRunForge_Help(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		t.Run(arg, func(t *testing.T) {
			if err := runForge([]string{arg}); err != nil {
				t.Errorf("runForge([%q]) = %v, want nil", arg, err)
			}
		})
	}
}

// TestRunForge_UnknownCommand is the Release Gate's own failure case at
// the unit level: an unregistered subcommand name must return
// errUnknownCommand, the same sentinel the top-level dispatch in run()
// uses, so main.go's errors.Is check maps it to the same exit code.
func TestRunForge_UnknownCommand(t *testing.T) {
	err := runForge([]string{"bogus"})
	if !errors.Is(err, errUnknownCommand) {
		t.Errorf("runForge([\"bogus\"]) = %v, want errUnknownCommand", err)
	}
}

// TestRunForge_DispatchesToRegisteredSubcommand proves the map lookup
// itself works and forwards both the remaining arguments and the
// handler's own return value unchanged. forgeCommands has no real entries
// yet (Phases 31-37 add them), so this registers a temporary fake one and
// removes it afterward, leaving the map exactly as every other test sees
// it.
func TestRunForge_DispatchesToRegisteredSubcommand(t *testing.T) {
	var gotArgs []string
	wantErr := errors.New("fake subcommand error")
	forgeCommands["fake-subcommand"] = func(args []string) error {
		gotArgs = args
		return wantErr
	}
	t.Cleanup(func() { delete(forgeCommands, "fake-subcommand") })

	err := runForge([]string{"fake-subcommand", "a", "b"})
	if !errors.Is(err, wantErr) {
		t.Errorf("runForge returned %v, want %v", err, wantErr)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "a" || gotArgs[1] != "b" {
		t.Errorf("fake subcommand received %v, want [a b]", gotArgs)
	}
}

// TestForgeRegisteredInCommands confirms main.go's own commands map wires
// "forge" to runForge, the one map entry Phase 30 adds there.
func TestForgeRegisteredInCommands(t *testing.T) {
	fn, ok := commands["forge"]
	if !ok {
		t.Fatal(`commands["forge"] is not registered`)
	}
	if fn == nil {
		t.Fatal(`commands["forge"] is nil`)
	}
}
