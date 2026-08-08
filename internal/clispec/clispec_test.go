package clispec

import "testing"

// walk visits spec and every descendant, depth-first, so a single test
// can assert an invariant across the whole tree without hand-listing
// every command.
func walk(spec Command, visit func(Command)) {
	visit(spec)
	for _, c := range spec.Subcommands {
		walk(c, visit)
	}
}

func TestRoot_EveryCommandHasNameAndSynopsis(t *testing.T) {
	walk(Root, func(c Command) {
		if c.Name == "" {
			t.Errorf("a command in the tree has an empty Name (parent chain unavailable in this walk; check Root's own literal)")
		}
		if c.Synopsis == "" {
			t.Errorf("command %q has no Synopsis", c.Name)
		}
	})
}

func TestRoot_EveryFlagHasNameTypeAndDoc(t *testing.T) {
	walk(Root, func(c Command) {
		for _, f := range c.Flags {
			if f.Name == "" {
				t.Errorf("command %q has a flag with an empty Name", c.Name)
			}
			if f.Type == "" {
				t.Errorf("command %q flag %q has an empty Type", c.Name, f.Name)
			}
			if f.Doc == "" {
				t.Errorf("command %q flag %q has an empty Doc", c.Name, f.Name)
			}
		}
	})
}

func TestRoot_NoDuplicateSiblingNames(t *testing.T) {
	walk(Root, func(c Command) {
		seen := map[string]bool{}
		for _, sub := range c.Subcommands {
			if seen[sub.Name] {
				t.Errorf("command %q has two subcommands both named %q", c.Name, sub.Name)
			}
			seen[sub.Name] = true
		}
	})
}

func TestFind(t *testing.T) {
	forge, ok := Find(Root, "forge")
	if !ok {
		t.Fatal("Find(Root, \"forge\") = _, false, want true")
	}
	if forge.Name != "forge" {
		t.Errorf("Find(Root, \"forge\").Name = %q, want \"forge\"", forge.Name)
	}
	if len(forge.Subcommands) == 0 {
		t.Error("forge should have subcommands (new-device, new-collection, new-plugin)")
	}

	if _, ok := Find(Root, "does-not-exist"); ok {
		t.Error("Find(Root, \"does-not-exist\") = _, true, want false")
	}
}

func TestRenderList(t *testing.T) {
	got := RenderList([]Command{
		{Name: "a", Synopsis: "short"},
		{Name: "longer-name", Synopsis: "another"},
	})
	want := "  a            short\n  longer-name  another\n"
	if got != want {
		t.Errorf("RenderList() =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderList_Empty(t *testing.T) {
	if got := RenderList(nil); got != "" {
		t.Errorf("RenderList(nil) = %q, want \"\"", got)
	}
}
