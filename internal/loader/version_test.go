// Package loader: tests of the engine version constraint and the describe
// decoder.
package loader

import (
	"strings"
	"testing"
)

// TestCheckEngineVersion is the whole decision table for a method's engine
// constraint against the running build (IMPLEMENTATION.md Phase 42's
// engine version decision): a development build (dev, empty, a stamped
// 0.0.0-dev+<commit>, anything with a dev prerelease or no version at all)
// loads a constrained method unchecked; a release, a release candidate
// included, enforces it; a malformed constraint is refused on every build.
func TestCheckEngineVersion(t *testing.T) {
	cases := []struct {
		constraint, running string
		wantErr             string
		wantUnchecked       bool
	}{
		{constraint: "", running: "1.0.0"},
		{constraint: "", running: "dev"},

		// Development builds: loaded, and the constraint reported unchecked.
		{constraint: ">=0.3.0", running: "dev", wantUnchecked: true},
		{constraint: ">=1.0.0", running: "", wantUnchecked: true},
		{constraint: ">=1.0.0", running: "0.0.0-dev+abc123def456", wantUnchecked: true},
		{constraint: ">=1.0.0", running: "0.0.0-dev+abc123def456.dirty", wantUnchecked: true},
		{constraint: ">=1.0.0", running: "0.0.0-dev", wantUnchecked: true},
		{constraint: ">=1.0.0", running: "1.3.0-dev", wantUnchecked: true},
		{constraint: ">=1.2.3", running: "nightly", wantUnchecked: true},

		// Releases enforce.
		{constraint: ">=0.3.0", running: "0.3.0"},
		{constraint: ">=1.0.0", running: "0.3.0", wantErr: "requires engine >=1.0.0, and this build is 0.3.0"},
		{constraint: ">=0.3.0", running: "1.0.0-rc1"},
		{constraint: ">=1.0.0", running: "1.0.0-rc1"},
		{constraint: ">=1.0.1", running: "1.0.0-rc1", wantErr: "this build is 1.0.0"},
		{constraint: ">=1.2.3", running: "v1.10.0"},
		{constraint: ">= v1.2.3", running: "2.0.0"},
		{constraint: ">=1.2.3", running: "1.2.3-rc1+build7"},
		{constraint: ">=2.0.0", running: "1.99.99", wantErr: "this build is 1.99.99"},

		// Malformed constraints are refused whatever the build.
		{constraint: ">=banana", running: "0.0.0-dev+abc", wantErr: "unsupported engine version constraint"},
		{constraint: "~1.2", running: "0.3.0", wantErr: "unsupported"},
		{constraint: "<=1.2.3", running: "1.0.0", wantErr: "unsupported"},
		{constraint: ">=1.2", running: "dev", wantErr: "unsupported"},
		{constraint: ">=1.02.3", running: "1.0.0", wantErr: "unsupported"},
		{constraint: ">=-1.2.3", running: "1.0.0", wantErr: "unsupported"},
		{constraint: ">=1.2.3.4", running: "1.0.0", wantErr: "unsupported"},
	}
	for _, tc := range cases {
		unchecked, err := checkEngineVersion(tc.constraint, tc.running)
		switch {
		case tc.wantErr != "":
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%q on %q: err %v, want %q", tc.constraint, tc.running, err, tc.wantErr)
			}
		case err != nil:
			t.Errorf("%q on %q: unexpected error %v", tc.constraint, tc.running, err)
		case unchecked != tc.wantUnchecked:
			t.Errorf("%q on %q: unchecked = %v, want %v", tc.constraint, tc.running, unchecked, tc.wantUnchecked)
		}
	}
}

// TestParseDescription_Refusals pins each message a bad describe document
// gets, since an author reads these to fix their program.
func TestParseDescription_Refusals(t *testing.T) {
	cases := map[string]string{
		"":                                 "printed nothing",
		"   \n":                            "printed nothing",
		"{":                                "invalid JSON",
		"[]":                               "invalid JSON",
		`{"protocol":1,"methods":[]}`:      "lists no methods",
		`{"protocol":7,"methods":[{}]}`:    "protocol 7",
		`{"protocol":1,"methods":[{}]} {}`: "data after",
	}
	for input, want := range cases {
		if _, err := parseDescription([]byte(input)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parseDescription(%q) = %v, want %q", input, err, want)
		}
	}
	// Unknown fields are accepted: a newer program within the same
	// protocol may send one this build does not know.
	if _, err := parseDescription([]byte(`{"protocol":1,"future":true,"methods":[{"name":"a.b","later":1}]}`)); err != nil {
		t.Errorf("an unknown field was refused: %v", err)
	}
}

// TestClip proves an untrusted string quoted into an error is bounded and
// never cut partway through a UTF-8 sequence.
func TestClip(t *testing.T) {
	if got := clip("short", 10); got != "short" {
		t.Errorf("clip left a short string as %q", got)
	}
	long := strings.Repeat("é", 100)
	got := clip(long, 11)
	if len(got) > 14 || !strings.HasSuffix(got, "...") || strings.ContainsRune(got, '�') {
		t.Errorf("clip(%d bytes, 11) = %q", len(long), got)
	}
}

// FuzzParseDescription feeds the describe decoder arbitrary bytes, the
// input an untrusted program controls completely. The only contract is
// that it returns a description or an error and never panics, and that
// anything it accepts has at least one method at this build's protocol.
func FuzzParseDescription(f *testing.F) {
	f.Add([]byte(`{"protocol":1,"methods":[{"name":"a.b","manifest":{"status":"implemented"}}]}`))
	f.Add([]byte(``))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"protocol":1,"methods":null}`))
	f.Add([]byte(`{"protocol":1,"methods":[{"manifest":{"requiredCapabilities":[1,2]}}]}`))
	f.Add([]byte(strings.Repeat("[", 256)))
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := parseDescription(data)
		if err != nil {
			return
		}
		if len(d.Methods) == 0 {
			t.Fatalf("accepted a description with no methods: %q", data)
		}
		for _, m := range d.Methods {
			// validateMethod is the next thing to see a decoded method, so
			// it must be panic-free on anything the decoder accepted.
			_, _ = validateMethod(m, "dev", nil)
		}
	})
}
