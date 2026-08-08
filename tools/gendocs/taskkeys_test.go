package main

import "testing"

// TestCheckTaskKeysComplete proves the current source tree's
// taskKeyDescriptions and engine.ReservedTaskKeys agree exactly. This is
// the same check generateTaskKeys runs before every regeneration; a
// dedicated test means a drift (a parser key added without a description,
// or a stale description left behind after a key was removed) fails `go
// test` directly instead of only being caught the next time someone
// happens to run the generator.
func TestCheckTaskKeysComplete(t *testing.T) {
	if err := checkTaskKeysComplete(); err != nil {
		t.Error(err)
	}
}

func TestCheckRunbookSchemaComplete(t *testing.T) {
	if err := checkRunbookSchemaComplete(); err != nil {
		t.Error(err)
	}
}

func TestTaskKeyTable(t *testing.T) {
	got := taskKeyTable([]taskKeyDoc{{"name", "a label"}})
	want := "| Key | Description |\n| --- | --- |\n| `name` | a label |\n"
	if got != want {
		t.Errorf("taskKeyTable() =\n%q\nwant\n%q", got, want)
	}
}
