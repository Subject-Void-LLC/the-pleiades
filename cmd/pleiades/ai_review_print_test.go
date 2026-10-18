// What a review shows a person, when its file was written by someone else.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestPrintReview_NoFieldActsOnTheTerminal: a review file from a cloned
// project can hold control sequences in any field; none of them reaches the
// terminal a person decides at, where a cursor movement could overwrite the
// lines saying why the answer was held.
func TestPrintReview_NoFieldActsOnTheTerminal(t *testing.T) {
	esc := "\x1b[2K\r"
	r := sdk.Review{
		Key: "k" + esc, Status: sdk.ReviewStatus("pending" + esc), Method: "ai.chat" + esc, Provider: "p" + esc, Model: "m" + esc,
		HeldAt: "2026-10-17T00:00:00Z" + esc, DecidedBy: "d" + esc, DecidedAt: "t" + esc, Comment: "c" + esc,
		Reasons: []string{"why" + esc}, Answer: "a" + esc,
		Usage: map[string]any{"cost": "1" + esc, "currency": "USD" + esc},
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	printReview(f, r)
	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(string(got), 0x1b) || strings.Contains(string(got), "\r") {
		t.Errorf("a control character reached the output: %q", got)
	}
}
