// Tests that add-host and set-host spell their tag flags both ways, driven
// through the real commands against a real project directory and read back
// through the repository run loads from.
package main

import (
	"slices"
	"testing"
)

// tagsOf returns name's tags, sorted, as a run would see them.
func tagsOf(t *testing.T, dir, name string) []string {
	t.Helper()
	var out []string
	for _, tag := range loadHost(t, dir, name).Tags() {
		out = append(out, string(tag))
	}
	slices.Sort(out)
	return out
}

// TestAddHost_TagAndTagsAreOneFlag checks every spelling add-host took before
// still works, --tag works too, and either repeats and mixes with commas.
func TestAddHost_TagAndTagsAreOneFlag(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		flags []string
		want  []string
	}{
		"h-old":    {[]string{"--tags", "a,b"}, []string{"a", "b"}},
		"h-new":    {[]string{"--tag", "a,b"}, []string{"a", "b"}},
		"h-repeat": {[]string{"--tag", "a", "--tags", "b", "--tag", "c,d"}, []string{"a", "b", "c", "d"}},
		"h-empty":  {[]string{"--tags", ""}, nil},
		"h-none":   {nil, nil},
	} {
		args := append([]string{name, "--dir", dir, "--type", "linux_server", "--set", "host=h"}, tc.flags...)
		if err := runAddHost(args); err != nil {
			t.Fatalf("%s: add-host %v: %v", name, tc.flags, err)
		}
		if got := tagsOf(t, dir, name); !slices.Equal(got, tc.want) {
			t.Errorf("%s: tags %v, want %v", name, got, tc.want)
		}
	}
}

// TestSetHost_TagsIsTagAndUntagStaysItsOwn checks set-host takes --tags for
// --tag, that both land in the one list, and that --untag removes a tag.
func TestSetHost_TagsIsTagAndUntagStaysItsOwn(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if err := runAddHost([]string{"h", "--dir", dir, "--type", "linux_server", "--set", "host=h"}); err != nil {
		t.Fatal(err)
	}
	if err := runSetHost([]string{"h", "--dir", dir, "--tags", "a,b", "--tag", "c"}); err != nil {
		t.Fatal(err)
	}
	if got, want := tagsOf(t, dir, "h"), []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Fatalf("tags %v, want %v", got, want)
	}
	if err := runSetHost([]string{"h", "--dir", dir, "--untag", "b"}); err != nil {
		t.Fatal(err)
	}
	if got, want := tagsOf(t, dir, "h"), []string{"a", "c"}; !slices.Equal(got, want) {
		t.Errorf("after --untag b: tags %v, want %v", got, want)
	}
	// A tag both added and removed is still the contradiction it was.
	if err := runSetHost([]string{"h", "--dir", dir, "--tags", "z", "--untag", "z"}); err == nil {
		t.Error("a tag both added with --tags and removed was accepted")
	}
}
