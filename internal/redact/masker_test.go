package redact_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// TestDefaultRulesetParses is the CI-side guard for DefaultRuleset's panic.
// The ruleset is embedded at build time, so a malformed one would take down
// every binary at startup. This is where that is discovered instead.
func TestDefaultRulesetParses(t *testing.T) {
	t.Parallel()

	rs := redact.DefaultRuleset()
	if len(rs.Rules) == 0 {
		t.Fatal("the embedded ruleset has no rules")
	}
	if len(rs.Comment) == 0 {
		t.Error("the embedded ruleset has no _comment block, which is where its honest status is recorded")
	}

	// Both channels must be present. A ruleset that lost all its key rules
	// would still parse, still validate, and silently stop masking the
	// channel that catches a secret this process does not know the bytes of.
	var keys, patterns int
	for _, r := range rs.Rules {
		switch r.Kind {
		case redact.KindKey:
			keys++
		case redact.KindPattern:
			patterns++
		}
	}
	if keys == 0 {
		t.Error("the embedded ruleset has no key rules")
	}
	if patterns == 0 {
		t.Error("the embedded ruleset has no pattern rules")
	}

	if _, err := redact.NewMasker(rs); err != nil {
		t.Fatalf("the embedded ruleset does not compile: %v", err)
	}
}

// TestRulesetValidationRefusals covers the malformed shapes, each of which
// would otherwise be a security control that parses cleanly and protects
// nothing.
func TestRulesetValidationRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
	}{
		{
			name: "no rules at all",
			json: `{"rules":[]}`,
		},
		{
			name: "a rule with no name",
			json: `{"rules":[{"kind":"key","keys":["password"],"description":"d"}]}`,
		},
		{
			name: "two rules sharing a name",
			json: `{"rules":[
				{"name":"a","kind":"key","keys":["password"],"description":"d"},
				{"name":"a","kind":"key","keys":["token"],"description":"d"}]}`,
		},
		{
			name: "a rule with no description, which is a rule nobody can safely remove later",
			json: `{"rules":[{"name":"a","kind":"key","keys":["password"]}]}`,
		},
		{
			name: "an unknown kind",
			json: `{"rules":[{"name":"a","kind":"literal","description":"d"}]}`,
		},
		{
			name: "a key rule naming no keys",
			json: `{"rules":[{"name":"a","kind":"key","keys":[],"description":"d"}]}`,
		},
		{
			name: "a key rule with an empty key, which would match every attribute",
			json: `{"rules":[{"name":"a","kind":"key","keys":[""],"description":"d"}]}`,
		},
		{
			name: "a key rule carrying a pattern as well",
			json: `{"rules":[{"name":"a","kind":"key","keys":["p"],"pattern":"x","description":"d"}]}`,
		},
		{
			name: "a pattern rule with no pattern",
			json: `{"rules":[{"name":"a","kind":"pattern","description":"d"}]}`,
		},
		{
			name: "a pattern rule carrying keys as well",
			json: `{"rules":[{"name":"a","kind":"pattern","pattern":"x","keys":["p"],"description":"d"}]}`,
		},
		{
			name: "an unknown field, which is how a rule written as key instead of keys masks nothing",
			json: `{"rules":[{"name":"a","kind":"key","key":["password"],"description":"d"}]}`,
		},
		{
			name: "malformed json",
			json: `{"rules":`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := redact.Parse([]byte(tt.json)); err == nil {
				t.Fatalf("Parse(%s) succeeded, want a refusal", tt.json)
			}
		})
	}
}

// TestNewMaskerRefusesAnUncompilablePattern covers the one failure Parse
// cannot catch, since Validate checks the pattern is present rather than
// that RE2 accepts it.
func TestNewMaskerRefusesAnUncompilablePattern(t *testing.T) {
	t.Parallel()

	rs := redact.Ruleset{Rules: []redact.Rule{{
		Name: "broken", Kind: redact.KindPattern, Pattern: "([unclosed", Description: "d",
	}}}

	if _, err := redact.NewMasker(rs); err == nil {
		t.Fatal("NewMasker() accepted a pattern RE2 cannot compile")
	}
}

// TestTextMasksAllThreeChannels proves each channel independently, then
// together. The channels overlap by design, so a test that only exercised
// them together could not tell which one was doing the work.
func TestTextMasksAllThreeChannels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		registered []string
		extra      []string
		text       string
		mustHide   []string
		mustKeep   []string
	}{
		{
			name:       "by value, registered globally",
			registered: []string{"global-secret-value"},
			text:       "using global-secret-value now",
			mustHide:   []string{"global-secret-value"},
			mustKeep:   []string{"using", "now"},
		},
		{
			name:     "by value, supplied by the caller for this line only",
			extra:    []string{"per-call-secret-value"},
			text:     "using per-call-secret-value now",
			mustHide: []string{"per-call-secret-value"},
		},
		{
			name:     "by shape, a PEM block nobody registered",
			text:     "key:\n-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAK\n-----END RSA PRIVATE KEY-----\ndone",
			mustHide: []string{"MIIBOgIBAAJBAK", "BEGIN RSA PRIVATE KEY"},
			mustKeep: []string{"key:", "done"},
		},
		{
			name:     "by shape, a bearer token",
			text:     "Authorization: Bearer abc123DEF456ghi789",
			mustHide: []string{"abc123DEF456ghi789"},
		},
		{
			name:     "by shape, a JWT",
			text:     "cookie=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.dBjftJeZ4CVP",
			mustHide: []string{"eyJhbGciOiJIUzI1NiJ9", "dBjftJeZ4CVP"},
			mustKeep: []string{"cookie="},
		},
		{
			name:     "by shape, an AWS access key id",
			text:     "id=AKIAIOSFODNN7EXAMPLE region=us-east-1",
			mustHide: []string{"AKIAIOSFODNN7EXAMPLE"},
			mustKeep: []string{"us-east-1"},
		},
		{
			name:     "by shape, a temporary AWS key id prefix",
			text:     "id=ASIAIOSFODNN7EXAMPLE",
			mustHide: []string{"ASIAIOSFODNN7EXAMPLE"},
		},
		{
			name:     "by shape, credentials embedded in a URL",
			text:     "dsn=postgres://admin:hunter2@db.internal:5432/pleiades",
			mustHide: []string{"admin:hunter2"},
			mustKeep: []string{"db.internal:5432/pleiades"},
		},
		{
			name:       "all channels at once",
			registered: []string{"registered-secret-value"},
			extra:      []string{"per-call-secret-value"},
			text:       "a registered-secret-value b per-call-secret-value c AKIAIOSFODNN7EXAMPLE d",
			mustHide:   []string{"registered-secret-value", "per-call-secret-value", "AKIAIOSFODNN7EXAMPLE"},
			mustKeep:   []string{"a ", " b ", " c ", " d"},
		},
		{
			name:     "text with nothing to mask is returned unchanged",
			text:     "device router-1 reachable in 4ms",
			mustKeep: []string{"device router-1 reachable in 4ms"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, err := redact.NewMasker(redact.DefaultRuleset())
			if err != nil {
				t.Fatalf("NewMasker() failed: %v", err)
			}
			m.Literals().Add(tt.registered...)

			got := m.Text(tt.extra, tt.text)
			for _, hidden := range tt.mustHide {
				if strings.Contains(got, hidden) {
					t.Errorf("output still contains %q: %q", hidden, got)
				}
			}
			for _, kept := range tt.mustKeep {
				if !strings.Contains(got, kept) {
					t.Errorf("output lost %q, so masking destroyed context: %q", kept, got)
				}
			}
		})
	}
}

// TestTextMasksLongestFirstAcrossBothLiteralSources guards a real seam.
//
// Registered literals and caller-supplied ones arrive from different places
// and are combined into one list before masking. If they were masked
// separately, a caller-supplied password that is a prefix of a registered
// passphrase could carve the passphrase in half and leave its tail exposed,
// which is the exact failure the longest-first rule exists to prevent.
func TestTextMasksLongestFirstAcrossBothLiteralSources(t *testing.T) {
	t.Parallel()

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}

	const short = "abcdefgh"
	const long = "abcdefghijklmnop"

	// Deliberately crossed: the long one registered, the short one supplied
	// per call, so the combination has to happen before sorting.
	m.Literals().Add(long)

	got := m.Text([]string{short}, "login "+long+" now")
	if strings.Contains(got, "ijklmnop") {
		t.Fatalf("the longer secret's tail leaked next to the placeholder: %q", got)
	}
}

// TestLiterals covers the set's own rules.
func TestLiterals(t *testing.T) {
	t.Parallel()

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}
	lits := m.Literals()

	t.Run("a value shorter than the minimum is refused", func(t *testing.T) {
		// Scrubbing "true" or "22" out of every later line would corrupt
		// unrelated output for the rest of the process, which is worse
		// than not masking at all.
		if added := lits.Add("short"); added != 0 {
			t.Errorf("Add(short value) recorded %d, want 0", added)
		}
	})

	t.Run("an empty value is refused", func(t *testing.T) {
		if added := lits.Add(""); added != 0 {
			t.Errorf("Add(\"\") recorded %d, want 0", added)
		}
	})

	t.Run("a long enough value is recorded once", func(t *testing.T) {
		if added := lits.Add("long-enough-secret"); added != 1 {
			t.Errorf("Add() recorded %d, want 1", added)
		}
		if added := lits.Add("long-enough-secret"); added != 0 {
			t.Errorf("re-adding recorded %d, want 0", added)
		}
	})

	t.Run("Forget removes a value", func(t *testing.T) {
		lits.Add("forgettable-secret")
		before := lits.Len()
		lits.Forget("forgettable-secret")
		if lits.Len() != before-1 {
			t.Errorf("Len() = %d after Forget, want %d", lits.Len(), before-1)
		}
		// Forgetting something absent is a no-op rather than a panic.
		lits.Forget("never-added-at-all")
	})

	t.Run("Snapshot is sorted longest first", func(t *testing.T) {
		fresh, err := redact.NewMasker(redact.DefaultRuleset())
		if err != nil {
			t.Fatalf("NewMasker() failed: %v", err)
		}
		fresh.Literals().Add("shortest-one", "a-much-longer-secret-value", "middle-length-x")

		snap := fresh.Literals().Snapshot()
		for i := 1; i < len(snap); i++ {
			if len(snap[i-1]) < len(snap[i]) {
				t.Fatalf("Snapshot() is not longest first: %v", snap)
			}
		}
	})
}

// TestLiteralsIsSafeForConcurrentUse exercises the set the way a fan-out
// does: many goroutines registering and forgetting while others mask.
// Meaningful under -race, which is how CI runs it.
func TestLiteralsIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()

	m, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() failed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := strings.Repeat("s", 10) + string(rune('a'+i))
			m.Literals().Add(v)
			_ = m.Text(nil, "line containing "+v)
			m.Literals().Snapshot()
			m.Literals().Forget(v)
		}(i)
	}
	wg.Wait()
}

// TestSharedReturnsOneInstance pins the singleton, which is the property
// the whole design rests on: a second instance would carry a second
// Literals set, and a secret registered with one and logged through the
// other would not be masked.
func TestSharedReturnsOneInstance(t *testing.T) {
	t.Parallel()

	if redact.Shared() != redact.Shared() {
		t.Fatal("Shared() returned two instances")
	}
}

// TestRulesetHasExactlyOneCopy is the guard for the "applied in Go and
// Python" requirement.
//
// The point of shipping the ruleset as data is that both languages read the
// same bytes. A second copy anywhere in the tree defeats that immediately,
// because the two would be edited independently and nothing would notice
// until a secret leaked through whichever half was stale.
//
// The legacy runner image gets its copy by a COPY instruction at build
// time, from this one file, rather than by a checked-in duplicate.
func TestRulesetHasExactlyOneCopy(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	var found []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory is not this test's problem, and
			// failing on one would make the guard flaky rather than strict.
			return nil //nolint:nilerr // deliberate: skip what cannot be read
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.Contains(d.Name(), "rules.json") || strings.Contains(d.Name(), "redact-rules.json") {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository failed: %v", err)
	}

	if len(found) != 1 {
		t.Fatalf("expected exactly one masking ruleset file, found %d: %v", len(found), found)
	}
	if filepath.Base(filepath.Dir(found[0])) != "redact" {
		t.Errorf("the ruleset lives at %s, want it inside internal/redact", found[0])
	}
}

// TestDefaultJSONReturnsACopy guards the raw-bytes accessor. It hands out
// the bytes the binary compiled in, and those bytes back a package-level
// variable, so returning the slice itself would let one caller's write
// corrupt the ruleset for the whole process.
func TestDefaultJSONReturnsACopy(t *testing.T) {
	t.Parallel()

	first := redact.DefaultJSON()
	if len(first) == 0 {
		t.Fatal("DefaultJSON() returned nothing")
	}
	first[0] = 'X'

	if redact.DefaultJSON()[0] == 'X' {
		t.Error("mutating one DefaultJSON() result changed the next")
	}
}

// repoRoot walks up from the working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() failed: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the module root")
		}
		dir = parent
	}
}
