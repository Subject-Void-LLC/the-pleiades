// Tests that a package that did not compile is reported with the
// compiler's own message, which go test -json gives only in build events.
package flakegate

import (
	"encoding/json"
	"strings"
	"testing"
)

// brokenBuild is go test -json's stream for a package whose source does not
// compile (go 1.26, captured from a module holding one), with a dependency
// that failed to build for want of disk space beside it: the compiler's
// words arrive only as build-output events named by ImportPath, never as
// output events named by Package.
const brokenBuild = `{"ImportPath":"example.com/buildfail/broken [example.com/buildfail/broken.test]","Action":"build-output","Output":"# example.com/buildfail/broken [example.com/buildfail/broken.test]\n"}
{"ImportPath":"example.com/buildfail/broken [example.com/buildfail/broken.test]","Action":"build-output","Output":"broken/b.go:3:23: undefined: undefinedName\n"}
{"ImportPath":"example.com/buildfail/broken [example.com/buildfail/broken.test]","Action":"build-fail"}
{"ImportPath":"compress/bzip2","Action":"build-output","Output":"# compress/bzip2\n"}
{"ImportPath":"compress/bzip2","Action":"build-output","Output":"compile: writing output: write $WORK/b123/_pkg_.a: no space left on device\n"}
{"ImportPath":"compress/bzip2","Action":"build-fail"}
{"Action":"start","Package":"example.com/buildfail/broken"}
{"Action":"output","Package":"example.com/buildfail/broken","Output":"FAIL\texample.com/buildfail/broken [build failed]\n"}
{"Action":"fail","Package":"example.com/buildfail/broken","Elapsed":0,"FailedBuild":"example.com/buildfail/broken [example.com/buildfail/broken.test]"}`

// TestFailureOutput_ABuildFailureCarriesTheCompilersWords: every build
// failure Classify finds is reported with what the compiler said, the
// package's own and a dependency's alike. Before, each read "FAIL ... [build
// failed]" or nothing, and on 2026-10-19 bsd-check's full disk failed 169
// packages that way with no reason anywhere in pr-check's report.
func TestFailureOutput_ABuildFailureCarriesTheCompilersWords(t *testing.T) {
	var events []Event
	for _, line := range strings.Split(brokenBuild, "\n") {
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	hard, _ := Classify(events, nil)
	want := map[string]string{
		"example.com/buildfail/broken": "undefined: undefinedName",
		"compress/bzip2":               "no space left on device",
	}
	found := 0
	for _, f := range hard {
		w, ok := want[f.Package]
		if !ok {
			continue
		}
		found++
		if f.Kind != FailedBuild {
			t.Errorf("%s: kind %s, want a build failure", f.Package, f.Kind)
		}
		if out := FailureOutput(events, f, 20); !strings.Contains(out, w) {
			t.Errorf("%s's output is %q, want it to hold %q", f.Package, out, w)
		}
	}
	if found != len(want) {
		t.Errorf("Classify found %+v, want a build failure for each of %v", hard, want)
	}
}
