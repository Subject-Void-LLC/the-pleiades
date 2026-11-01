// Tests of a run's limit: Ansible's --limit grammar and what it admits.
package engine_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// limitHosts is a small fleet: name and groups.
var limitHosts = []struct {
	name   string
	groups []string
}{
	{"web-1", []string{"web", "prod"}},
	{"web-2", []string{"web", "staging"}},
	{"db-1", []string{"db", "prod"}},
	{"edge", nil},
}

// admitted returns which of limitHosts text admits, joined.
func admitted(t *testing.T, text string) string {
	t.Helper()
	l, err := engine.ParseLimit(text)
	if err != nil {
		t.Fatalf("ParseLimit(%q): %v", text, err)
	}
	var out []string
	for _, h := range limitHosts {
		if l.Admits(h.name, h.groups) {
			out = append(out, h.name)
		}
	}
	return strings.Join(out, ",")
}

// TestLimit_AdmitsAsAnsibleDoes: names and groups union, globs on either,
// & keeps only what matches, ! removes what matches, the order patterns are
// written in does not matter, and no limit admits everyone.
func TestLimit_AdmitsAsAnsibleDoes(t *testing.T) {
	for text, want := range map[string]string{
		"":                  "web-1,web-2,db-1,edge",
		"all":               "web-1,web-2,db-1,edge",
		"web-1":             "web-1",
		"web":               "web-1,web-2",
		"web,db-1":          "web-1,web-2,db-1",
		"web:db":            "web-1,web-2,db-1",
		"web-*":             "web-1,web-2",
		"*-1":               "web-1,db-1",
		"web,&prod":         "web-1",
		"&prod,web":         "web-1",
		"all,!prod":         "web-2,edge",
		"!prod":             "web-2,edge",
		"web,!web-2":        "web-1",
		"prod,&web,!web-1":  "",
		" web , ! staging ": "web-1",
		"nothing":           "",
		"web,,db-1":         "web-1,web-2,db-1",
	} {
		if got := admitted(t, text); got != want {
			t.Errorf("limit %q admits %q, want %q", text, got, want)
		}
	}
}

// TestLimit_RefusesWhatItCannotRead, naming why.
func TestLimit_RefusesWhatItCannotRead(t *testing.T) {
	for text, want := range map[string]string{
		"!":                       "empty pattern",
		",":                       "only separators",
		",,:":                     "only separators",
		"~web.*":                  "regular expression",
		"@hosts.txt":              "from a file",
		"{{ vars.hosts }}":        "template",
		"web[":                    "not a valid glob",
		strings.Repeat("a", 5000): "more than the 4096",
		strings.Repeat("a,", 300): "more than the 256",
	} {
		if _, err := engine.ParseLimit(text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseLimit(%.20q): %v, want one saying %q", text, err, want)
		}
	}
}

// FuzzLimit: any text is refused or parses into a limit that answers for any
// device without panicking, and an exclusion of the device's own name always
// keeps it out.
func FuzzLimit(f *testing.F) {
	for _, seed := range []string{"web,&prod,!db-1", "*", "!*", "[a-", "a:b:c", "~x", "@f", "&", "web-[0-9]", ",", "::"} {
		f.Add(seed, "web-1", "web")
	}
	f.Fuzz(func(t *testing.T, text, name, group string) {
		l, err := engine.ParseLimit(text)
		if err != nil {
			return
		}
		_ = l.Admits(name, []string{group})
		// A pattern is trimmed, so a name that is not (one starting with a
		// form feed, say) cannot be named by one; no device name holds one.
		if name == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, ",:*?[]\\!&~@ \t\n\r\v\f{}%#") {
			return
		}
		excluded, err := engine.ParseLimit(text + ",!" + name)
		if err != nil {
			return
		}
		if excluded.Admits(name, []string{group}) {
			t.Fatalf("limit %q with !%s still admits %s", text, name, name)
		}
	})
}
