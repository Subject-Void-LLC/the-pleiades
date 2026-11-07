// Package main: tests of `runner version`.
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

func TestRunnerVersionNamesItsProtocols(t *testing.T) {
	var b bytes.Buffer
	if code := printVersion(&b, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if !strings.HasPrefix(lines[0], "runner ") {
		t.Errorf("first line %q, want the version a script reads", lines[0])
	}
	if last := lines[len(lines)-1]; last != "external collection protocols: "+external.SpokenText() {
		t.Errorf("last line %q", last)
	}

	b.Reset()
	if code := printVersion(&b, []string{"--json"}); code != 0 {
		t.Fatalf("--json exit %d", code)
	}
	var r clireport.VersionReport
	if err := json.Unmarshal(b.Bytes(), &r); err != nil {
		t.Fatalf("--json is not one document: %v\n%s", err, b.String())
	}
	if r.Program != "runner" || r.SchemaVersion != clireport.VersionReportVersion ||
		r.ExtensionProtocols.Current != external.ProtocolVersion || len(r.ExtensionProtocols.Versions) != len(external.Protocols) {
		t.Errorf("report %+v", r)
	}

	if code := printVersion(&b, []string{"--yaml"}); code != 2 {
		t.Errorf("an unknown flag exits %d, want 2", code)
	}
}
