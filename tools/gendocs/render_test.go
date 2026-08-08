package main

import "testing"

func TestMdEscape(t *testing.T) {
	cases := map[string]string{
		"plain":            "plain",
		"a|b":              "a\\|b",
		"line1\nline2":     "line1 line2",
		"a|b\nc|d":         "a\\|b c\\|d",
		"`code|span`\nfoo": "`code\\|span` foo",
	}
	for in, want := range cases {
		if got := mdEscape(in); got != want {
			t.Errorf("mdEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCode(t *testing.T) {
	if got := code(""); got != "-" {
		t.Errorf("code(\"\") = %q, want \"-\"", got)
	}
	if got := code("int"); got != "`int`" {
		t.Errorf("code(\"int\") = %q, want \"`int`\"", got)
	}
}

func TestYesNo(t *testing.T) {
	if got := yesNo(true); got != "yes" {
		t.Errorf("yesNo(true) = %q, want \"yes\"", got)
	}
	if got := yesNo(false); got != "no" {
		t.Errorf("yesNo(false) = %q, want \"no\"", got)
	}
}

func TestTable(t *testing.T) {
	got := table([]string{"A", "B"}, [][]string{
		{"1", "2"},
		{"pipe|here", "plain"},
	})
	want := "| A | B |\n" +
		"| --- | --- |\n" +
		"| 1 | 2 |\n" +
		"| pipe\\|here | plain |\n"
	if got != want {
		t.Errorf("table() =\n%q\nwant\n%q", got, want)
	}
}

func TestTable_NoRows(t *testing.T) {
	got := table([]string{"A", "B", "C"}, nil)
	want := "| A | B | C |\n| --- | --- | --- |\n"
	if got != want {
		t.Errorf("table() =\n%q\nwant\n%q", got, want)
	}
}

func TestFrontMatter(t *testing.T) {
	got := frontMatter("beta")
	want := "---\nstatus: beta\n---\n\n"
	if got != want {
		t.Errorf("frontMatter(\"beta\") = %q, want %q", got, want)
	}
}

func TestSortedKeys(t *testing.T) {
	m := map[string]int{"z": 1, "a": 2, "m": 3}
	got := sortedKeys(m)
	want := []string{"a", "m", "z"}
	if len(got) != len(want) {
		t.Fatalf("sortedKeys() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sortedKeys()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestQuoteList(t *testing.T) {
	if got := quoteList(nil); got != "-" {
		t.Errorf("quoteList(nil) = %q, want \"-\"", got)
	}
	if got := quoteList([]string{"ssh", "https"}); got != "`ssh`, `https`" {
		t.Errorf("quoteList(...) = %q, want \"`ssh`, `https`\"", got)
	}
}

func TestItoa(t *testing.T) {
	if got := itoa(42); got != "42" {
		t.Errorf("itoa(42) = %q, want \"42\"", got)
	}
	if got := itoa(0); got != "0" {
		t.Errorf("itoa(0) = %q, want \"0\"", got)
	}
}
