// Tests that `pleiades facts` shows a stored key that would not show as
// itself escaped, in its text and its --json view, and that --verify's break
// reason, which quotes a stored chain hash, reaches the terminal escaped.
// Through the real binary over a real project's file store.
package main_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
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
	store, err := facts.NewFileStore(filepath.Join(dir, ".pleiades", "facts"), projectFactKeys(dir))
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
	store, err := facts.NewFileStore(factsDir, projectFactKeys(dir))
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := facts.Hash("os.family", "debian")
	if err := store.Record(t.Context(), empty.DeviceID, facts.Observation{Entries: []facts.Entry{{Key: "os.family", Value: "debian", Hash: hash}}, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(factsDir, empty.DeviceID+".jsonl")
	// Edited as someone holding the project's key could: the line opened,
	// changed and sealed again.
	lines := factstest.OpenLines(t, projectFactKeys(dir), path, empty.DeviceID, facts.RoleValues)
	if len(lines) != 1 || !strings.Contains(string(lines[0]), `"prev":""`) {
		t.Fatalf("the history line: %s", lines)
	}
	// JSON's escape for ESC, built in code.
	esc := string(rune(0x5C)) + "u001b"
	lines[0] = []byte(strings.Replace(string(lines[0]), `"prev":""`, `"prev":"`+esc+`[31mzzzzzz"`, 1))
	factstest.WriteLines(t, projectFactKeys(dir), path, empty.DeviceID, facts.RoleValues, lines)
	out, err = runPleiades(t, dir, "facts", "web1", "--verify")
	if err == nil || !strings.Contains(out, "breaks") {
		t.Fatalf("--verify over an edited line: %v\n%s", err, out)
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("the break reason reached the terminal raw:\n%q", out)
	}
}

// projectFactKeys is the keys the project in dir seals its facts under, as
// the CLI derives them from the project's master key.
func projectFactKeys(dir string) *facts.Keys {
	return facts.NewLazyKeys(func() ([]byte, error) { return credential.ResolveMasterKey(dir) }, "project")
}

// TestCLI_FactsWithoutTheMasterKeySaysSo: a project's stored facts are sealed
// under its master key, so with the key gone `pleiades facts` and its
// --verify fail naming the key, show nothing, and do not make a new key that
// would open none of it.
func TestCLI_FactsWithoutTheMasterKeySaysSo(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "web1", "--type", "linux_server", "--set", "host=10.0.0.5"); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	out, _ := runPleiades(t, dir, "facts", "web1", "--json")
	var host struct {
		DeviceID string `json:"device_id"`
	}
	if json.Unmarshal([]byte(out), &host) != nil || host.DeviceID == "" {
		t.Fatalf("facts --json: %s", out)
	}
	store, err := facts.NewFileStore(filepath.Join(dir, ".pleiades", "facts"), projectFactKeys(dir))
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := facts.Prepare(map[string]any{"package.zq9sealed": "1.2.3-zq9"}, facts.Guard{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record(t.Context(), host.DeviceID, facts.Observation{Entries: entries, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// The control: with the key, the fact reads.
	if out, err := runPleiades(t, dir, "facts", "web1"); err != nil || !strings.Contains(out, "package.zq9sealed") {
		t.Fatalf("facts with the key: %v\n%s", err, out)
	}
	keyFile := filepath.Join(dir, ".pleiades", "master.key")
	if err := os.Remove(keyFile); err != nil {
		t.Fatalf("the project's key file: %v", err)
	}
	for _, args := range [][]string{{"facts", "web1"}, {"facts", "web1", "package.zq9sealed"}, {"facts", "web1", "--verify"}} {
		out, err := runPleiades(t, dir, args...)
		if err == nil || !strings.Contains(out+err.Error(), "master key") {
			t.Errorf("%v with no master key: %v\n%s; want an error naming the master key", args, err, out)
		}
		if strings.Contains(out, "zq9") {
			t.Errorf("%v with no master key showed a fact:\n%s", args, out)
		}
	}
	if _, err := os.Stat(keyFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a read made a new master key: %v", err)
	}
}
