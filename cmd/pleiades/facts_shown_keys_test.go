// Tests that `pleiades facts` shows a stored key that would not show as
// itself escaped, in its text and its --json view, and that --verify's break
// reason, which quotes a stored chain hash, reaches the terminal escaped.
// Through the real binary over a real project's file store.
package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// shownEscape is the escape ShownKey writes for a code point given in hex,
// built in code so this file's source holds no escape that reads as one.
func shownEscape(hex string) string { return string(rune(0x5C)) + "u{" + hex + "}" }

// TestCLI_FactsShowsKeysThatWouldNotShowAsThemselvesEscaped: a store a hand
// edit or another build wrote can hold keys holding a zero width space, a
// tag character, a line separator or (in a volatile key's file) a newline.
// `pleiades facts` names each with those characters escaped as \u{...}, in
// the text view and in --json alike, and never prints one raw.
func TestCLI_FactsShowsKeysThatWouldNotShowAsThemselvesEscaped(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "web1", "--type", "linux_server", "--set", "host=10.0.0.5"); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	out, err := runPleiades(t, dir, "facts", "web1", "--json")
	var empty struct {
		DeviceID string `json:"device_id"`
	}
	if err != nil || json.Unmarshal([]byte(out), &empty) != nil || empty.DeviceID == "" {
		t.Fatalf("facts --json on a host with none: %v\n%s", err, out)
	}
	stored := map[string]string{
		"pkg.a" + string(rune(0x200B)) + "b": "pkg.a" + shownEscape("200B") + "b",
		"pkg.t" + string(rune(0xE0041)):      "pkg.t" + shownEscape("E0041"),
		"pkg.l" + string(rune(0x2028)) + "x": "pkg.l" + shownEscape("2028") + "x",
		"vol\nkey":                           "vol" + shownEscape("A") + "key",
		"os.family":                          "os.family",
	}
	store, err := facts.NewFileStore(filepath.Join(dir, ".pleiades", "facts"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []facts.Entry
	for key := range stored {
		hash, err := facts.Hash(key, "v")
		if err != nil {
			t.Fatal(err)
		}
		kind := facts.KindStable
		if strings.HasPrefix(key, "vol") {
			kind = facts.KindVolatile
		}
		entries = append(entries, facts.Entry{Key: key, Value: "v", Hash: hash, Kind: kind})
	}
	if err := store.Record(t.Context(), empty.DeviceID, facts.Observation{Entries: entries, At: time.Now()}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	raw := []rune{0x200B, 0xE0041, 0x2028}

	text, err := runPleiades(t, dir, "facts", "web1")
	if err != nil {
		t.Fatalf("facts: %v\n%s", err, text)
	}
	for _, r := range raw {
		if strings.ContainsRune(text, r) {
			t.Errorf("the text view prints U+%04X raw:\n%q", r, text)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.HasPrefix(line, "key = ") {
			t.Errorf("a key's newline started a line of its own:\n%s", text)
		}
	}
	for _, shown := range stored {
		if !strings.Contains(text, shown+" = ") {
			t.Errorf("the text view does not name %q:\n%s", shown, text)
		}
	}

	out, err = runPleiades(t, dir, "facts", "web1", "--json")
	var report struct {
		Facts []struct {
			Key string `json:"key"`
		} `json:"facts"`
	}
	if err != nil || json.Unmarshal([]byte(out), &report) != nil {
		t.Fatalf("facts --json: %v\n%s", err, out)
	}
	var got, want []string
	for _, e := range report.Facts {
		got = append(got, e.Key)
	}
	for _, shown := range stored {
		want = append(want, shown)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("--json names %q, want %q", got, want)
	}
}

// TestCLI_FactsVerifyEscapesTheBreakReason: a history line edited to name a
// previous chain hash holding an escape sequence breaks the chain, and the
// break's reason, which quotes the start of that hash, reaches the terminal
// escaped rather than acted on.
func TestCLI_FactsVerifyEscapesTheBreakReason(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "web1", "--type", "linux_server", "--set", "host=10.0.0.5"); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	out, _ := runPleiades(t, dir, "facts", "web1", "--json")
	var empty struct {
		DeviceID string `json:"device_id"`
	}
	if json.Unmarshal([]byte(out), &empty) != nil || empty.DeviceID == "" {
		t.Fatalf("facts --json: %s", out)
	}
	factsDir := filepath.Join(dir, ".pleiades", "facts")
	store, err := facts.NewFileStore(factsDir)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := facts.Hash("os.family", "debian")
	if err := store.Record(t.Context(), empty.DeviceID, facts.Observation{Entries: []facts.Entry{{Key: "os.family", Value: "debian", Hash: hash}}, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(factsDir, empty.DeviceID+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `"prev":""`) {
		t.Fatalf("the history line: %v\n%s", err, data)
	}
	// JSON's escape for ESC, built in code.
	esc := string(rune(0x5C)) + "u001b"
	edited := strings.Replace(string(data), `"prev":""`, `"prev":"`+esc+`[31mzzzzzz"`, 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = runPleiades(t, dir, "facts", "web1", "--verify")
	if err == nil || !strings.Contains(out, "breaks") {
		t.Fatalf("--verify over an edited line: %v\n%s", err, out)
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("the break reason reached the terminal raw:\n%q", out)
	}
}
