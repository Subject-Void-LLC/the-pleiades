// Tests for reading and applying an ignore file.
package scanignore_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/scanignore"
)

// day is a date in UTC.
func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// TestJudge_SuppressesUntilItsDayThenReports: an entry suppresses a
// finding by any of its ids through the whole of its until day, then
// suppresses nothing and is reported; an entry no finding met is reported
// as unused.
func TestJudge_SuppressesUntilItsDayThenReports(t *testing.T) {
	f, err := scanignore.Parse([]byte(`
- id: GHSA-aaaa
  reason: the vulnerable path is unreachable
  until: 2026-12-31
- id: CVE-2026-1
  reason: fixed upstream, waiting on a release
  until: 2026-06-30
- id: GHSA-unused
  reason: was in an old dependency
  until: 2027-01-01
`))
	if err != nil {
		t.Fatal(err)
	}
	j := f.Judge(day("2026-12-31").Add(23 * time.Hour))
	if e, ok := j.Ignored("CVE-x", "GHSA-aaaa"); !ok || e.Reason != "the vulnerable path is unreachable" || e.Line != 2 {
		t.Errorf("an alias in date = %+v, %v", e, ok)
	}
	if _, ok := j.Ignored("CVE-2026-1"); ok {
		t.Error("an expired entry suppressed its finding")
	}
	if expired := j.Expired(); len(expired) != 1 || expired[0].ID != "CVE-2026-1" {
		t.Errorf("Expired = %v", expired)
	}
	if unused := j.Unused(); len(unused) != 1 || unused[0].ID != "GHSA-unused" {
		t.Errorf("Unused = %v", unused)
	}
	if _, ok := f.Judge(day("2027-01-01")).Ignored("GHSA-aaaa"); ok {
		t.Error("an entry suppressed the day after its until")
	}
}

// TestParse_RefusesAnIncompleteFileWhole names each problem by line.
func TestParse_RefusesAnIncompleteFileWhole(t *testing.T) {
	for src, want := range map[string]string{
		"- id: A\n  until: 2026-01-01\n":                                                         "line 1: A has no reason",
		"- id: A\n  reason: r\n":                                                                 "A has no until",
		"- reason: r\n  until: 2026-01-01\n":                                                     "an entry has no id",
		"- id: A\n  reason: r\n  until: next week\n":                                             `until "next week" is not a date`,
		"- id: A\n  reason: r\n  until: 2026-01-01\n  expires: never\n":                          `"expires" is not a key`,
		"- id: A\n  reason: r\n  until: 2026-01-01\n- id: A\n  reason: r\n  until: 2026-01-01\n": "A is already ignored at line 1",
		"id: A\n":                    "is a list of entries",
		"- A\n":                      "is not a mapping",
		"- id: A\n---\n- id: B\n":    "more than one YAML document",
		"\xff":                       "not UTF-8",
		strings.Repeat("#", 1<<20+1): "over",
	} {
		if _, err := scanignore.Parse([]byte(src)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%.40q) = %v, want %q", src, err, want)
		}
	}
	for _, empty := range []string{"", "# nothing ignored\n", "[]\n", "~\n"} {
		if f, err := scanignore.Parse([]byte(empty)); err != nil || len(f.Entries) != 0 {
			t.Errorf("Parse(%q) = %+v, %v", empty, f, err)
		}
	}
}
