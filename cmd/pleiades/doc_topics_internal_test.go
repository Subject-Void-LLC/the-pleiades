// Tests that doc filters and doc conditions list what the engine has: every
// template filter, described, and every condition library's functions, in
// both the text and the --json view.
package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

func TestDocTopics(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runDoc([]string{"filters"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, f := range render.Filters() {
		if f.Usage == "" || f.Description == "" {
			t.Errorf("filter %s has no usage or description", f.Name)
		}
		if !strings.Contains(out, f.Usage) {
			t.Errorf("doc filters leaves out %s", f.Name)
		}
	}
	if !strings.Contains(out, "regex_replace") || !strings.Contains(out, "secret.<name>.<field>") {
		t.Errorf("doc filters:\n%s", out)
	}

	var topic conditionsTopic
	out = captureStdout(t, func() {
		if err := runDoc([]string{"conditions", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal([]byte(out), &topic); err != nil {
		t.Fatal(err)
	}
	names := map[string]int{}
	for _, lib := range topic.Libraries {
		names[lib.Name] = len(lib.Functions)
	}
	for _, lib := range []string{"ext.Network", "ext.Encoders", "cel.OptionalTypes", "filters"} {
		if names[lib] == 0 {
			t.Errorf("doc conditions lists no function of %s: %v", lib, names)
		}
	}
	out = captureStdout(t, func() {
		if err := runDoc([]string{"conditions"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "when_or") || !strings.Contains(out, "cidr(string) -> net.CIDR") {
		t.Errorf("doc conditions:\n%s", out)
	}
}
