// Tests for the package's pure decisions and wording: which keys can read a
// census, what a DSN may carry, and what a tool's error may say.
package backup

import (
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// census builds a census of one column whose rows open under the named
// candidates with the tags given, plus unknown rows.
func census(tags map[string]map[string]int, unknown int) crypto.Census {
	col := crypto.ColumnCensus{Noun: "credentials", Opens: map[string]int{}, Tags: tags, Unknown: unknown}
	for name, byTag := range tags {
		for _, n := range byTag {
			col.Opens[name] += n
			col.Sealed += n
		}
	}
	col.Sealed += unknown
	return crypto.Census{Columns: []crypto.ColumnCensus{col}}
}

// TestKeySet_ReadableRequiresTheKeyAndItsTag covers the rotation case the
// integration tests do not: values under the previous key must carry the
// previous key's tag, not the current one's.
func TestKeySet_ReadableRequiresTheKeyAndItsTag(t *testing.T) {
	ks := keySet{
		key: []byte(strings.Repeat("c", 32)), version: "v2", name: "the key",
		previous: []byte(strings.Repeat("p", 32)), previousVersion: "v1", previousName: "the previous key",
	}
	if err := ks.readable(census(map[string]map[string]int{"the key": {"v2": 3}, "the previous key": {"v1": 2}}, 0), nil); err != nil {
		t.Fatalf("readable() refused a rotation's two keys under their own tags: %v", err)
	}
	err := ks.readable(census(map[string]map[string]int{"the previous key": {"v2": 1}}, 0), nil)
	if err == nil || !strings.Contains(err.Error(), "1 value opens under the previous key and carries the version tag v2, while .env gives that key the tag v1") {
		t.Fatalf("readable() error = %v, want the previous key's tag named", err)
	}
	err = ks.readable(census(nil, 2), []string{"aaaa-bbbb", "cccc-dddd"})
	if err == nil || !strings.Contains(err.Error(), "2 of them are under no key") || !strings.Contains(err.Error(), "lists keys aaaa-bbbb, cccc-dddd") {
		t.Fatalf("readable() error = %v, want the unknown count and the registry's keys", err)
	}
}

// TestTagOf_ReadsOneTagOrRefuses covers the tag a key entered at a restore
// is written with.
func TestTagOf_ReadsOneTagOrRefuses(t *testing.T) {
	ks := entered([]byte(strings.Repeat("e", 32)))
	if tag, err := tagOf(census(nil, 0), ks); err != nil || tag != crypto.DefaultKeyVersion {
		t.Fatalf("tagOf(no values) = %q, %v; want the default", tag, err)
	}
	if tag, err := tagOf(census(map[string]map[string]int{ks.name: {"v9": 4}}, 0), ks); err != nil || tag != "v9" {
		t.Fatalf("tagOf(one tag) = %q, %v", tag, err)
	}
	if _, err := tagOf(census(map[string]map[string]int{ks.name: {"v1": 1, "v2": 1}}, 0), ks); err == nil || !strings.Contains(err.Error(), "v1, v2") {
		t.Fatalf("tagOf(two tags) error = %v, want a refusal naming both", err)
	}
}

// TestRecordedKeys_WordsEveryCount pins the three sentences.
func TestRecordedKeys_WordsEveryCount(t *testing.T) {
	for shorts, want := range map[string]string{
		"":      "lists no key",
		"a":     "lists key a.",
		"a,b,c": "lists keys a, b, c.",
	} {
		var list []string
		if shorts != "" {
			list = strings.Split(shorts, ",")
		}
		if got := recordedKeys(list); !strings.Contains(got, want) {
			t.Errorf("recordedKeys(%q) = %q, want %q", shorts, got, want)
		}
	}
}

// TestParseTarget_PassesOnlyWhatItUnderstands covers the DSNs refused and
// the password kept out of the argument list.
func TestParseTarget_PassesOnlyWhatItUnderstands(t *testing.T) {
	good, err := parseTarget("postgres://pleiades:s3cret@postgres/pleiades?sslmode=disable&connect_timeout=5")
	if err != nil {
		t.Fatalf("parseTarget() error = %v", err)
	}
	if good.port != "5432" {
		t.Errorf("port = %q, want the default", good.port)
	}
	info := good.conninfo()
	if strings.Contains(info, "s3cret") {
		t.Fatal("conninfo, which is passed as an argument, holds the password")
	}
	if !strings.Contains(info, "connect_timeout='5' sslmode='disable'") {
		t.Errorf("conninfo() = %q, want the settings passed through in a fixed order", info)
	}
	for _, dsn := range []string{
		"sqlite://controller.db", "controller.db", "postgres:///pleiades",
		"postgres://pleiades@postgres/Pleiades", "postgres://u@postgres/db;drop",
		"postgres://pleiades@postgres/pleiades?options=-c%20search_path=evil",
	} {
		if _, err := parseTarget(dsn); err == nil {
			t.Errorf("parseTarget(%q) accepted it", dsn)
		}
	}
	if got := quoteConninfo(`it's a \\ value`); got != `'it\'s a \\\\ value'` {
		t.Errorf("quoteConninfo() = %s", got)
	}
}

// TestToolReason_HidesValuesAndKeepsTheReason covers what a failed client
// program's output may put in an error.
func TestToolReason_HidesValuesAndKeepsTheReason(t *testing.T) {
	stderr := "pg_restore: connecting\n" +
		`pg_restore: error: COPY failed for table "sessions": ERROR:  invalid input syntax for type bytea: "a-token-hash"` + "\n" +
		"CONTEXT:  COPY sessions, line 1\n"
	got := toolReason(stderr)
	if strings.Contains(got, "a-token-hash") || strings.Contains(got, "CONTEXT") {
		t.Fatalf("toolReason() = %q; it carries a value", got)
	}
	if !strings.Contains(got, "invalid input syntax for type bytea") || !strings.Contains(got, `"(value hidden)"`) {
		t.Fatalf("toolReason() = %q; the reason is gone", got)
	}
	if toolReason("nothing useful\n") != "" {
		t.Fatal("toolReason() invented a reason")
	}
}

// TestWords_FitTheCount pins the sentences that change with a count.
func TestWords_FitTheCount(t *testing.T) {
	for n, want := range map[int]string{0: "holds no sealed values", 1: "Its one sealed value opens", 7: "All 7 of its sealed values open"} {
		if got := readableLine(n, "the key"); !strings.Contains(got, want) {
			t.Errorf("readableLine(%d) = %q", n, got)
		}
	}
	for n, want := range map[int64]string{12: "12 bytes", 4096: "4 KB", 3 << 20: "3.0 MB", 5 << 30: "5.0 GB"} {
		if got := size(n); got != want {
			t.Errorf("size(%d) = %q, want %q", n, got, want)
		}
	}
	summary := restoredSummary("pleiades", "/b/x.dump", Name{}, false, census(map[string]map[string]int{"k": {"v1": 1}}, 0),
		keySet{key: []byte(strings.Repeat("k", 32)), version: "v1"}, Restored{FailedJobs: 2, EndedSessions: 1, KeyFile: "/s/.env"})
	for _, want := range []string{"held nothing, so nothing was set aside", "2 jobs running when the backup was taken were marked failed", "1 session from the backup was ended", "Wrote the key to /s/.env"} {
		if !strings.Contains(summary, want) {
			t.Errorf("restoredSummary() does not say %q:\n%s", want, summary)
		}
	}
	for _, importing := range []bool{true, false} {
		if !strings.Contains(keyAdvice(importing), "MASTER_ENCRYPTION_KEY_PREVIOUS") {
			t.Errorf("keyAdvice(%v) does not explain a rotation", importing)
		}
	}
	if age(90*time.Second) != "moments" || age(3*24*time.Hour) != "3 days" {
		t.Error("age() words")
	}
}
