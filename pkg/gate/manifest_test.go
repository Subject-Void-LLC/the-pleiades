// Tests that a manifest is decoded strictly and refused, naming the rule,
// for each way it can be wrong.
package gate

import (
	"strings"
	"testing"
	"time"
)

// goodManifest is a manifest that uses every field once.
const goodManifest = `version: 1
kinds:
  - name: browser
    description: needs a headless browser
defaults:
  timeout: 30m
  parallelism: 2
checks:
  - name: unit
    kind: pure
    command: [go, test, ./...]
    dir: .
    requires:
      commands: [go]
      env: [GOFLAGS]
      files: [go.mod]
      os: linux
      tcp: ["127.0.0.1:5432"]
  - name: lint
    kind: pure
    shell: make lint
    skip_exit: [5]
  - name: migrations
    kind: db
    heavy: true
    serial: true
    timeout: 20m
    shell: ./migrate.sh
    image: docker.io/library/postgres:16@sha256:` + digest + `
    network: gate-net
    pull: true
    requires:
      engine: true
  - name: ui
    kind: browser
    shell: npm test
`

// digest is a well-formed image digest.
const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestParse_ReadsEveryField decodes a manifest using every field.
func TestParse_ReadsEveryField(t *testing.T) {
	m, err := Parse([]byte(goodManifest))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Checks) != 4 || m.Defaults.Parallelism != 2 || time.Duration(m.Defaults.Timeout) != 30*time.Minute {
		t.Fatalf("manifest = %+v", m)
	}
	unit, mig := m.Checks[0], m.Checks[2]
	if len(unit.Command) != 3 || unit.Requires.OS != "linux" || unit.Requires.TCP[0] != "127.0.0.1:5432" {
		t.Errorf("unit = %+v", unit)
	}
	if !mig.Heavy || !mig.Serial || !mig.Pull || mig.Network != "gate-net" || !mig.Requires.Engine {
		t.Errorf("migrations = %+v", mig)
	}
	if m.TimeoutOf(unit) != 30*time.Minute || m.TimeoutOf(mig) != 20*time.Minute {
		t.Errorf("timeouts = %v, %v", m.TimeoutOf(unit), m.TimeoutOf(mig))
	}
	if unit.Requires.Empty() || !m.Checks[1].Requires.Empty() {
		t.Error("Requires.Empty disagrees with what the checks declare")
	}
}

// TestParse_RefusesEachBrokenRule changes one thing per case and expects
// the refusal to name it.
func TestParse_RefusesEachBrokenRule(t *testing.T) {
	check := func(body string) string {
		return "version: 1\ndefaults:\n  timeout: 1m\nchecks:\n" + body
	}
	for name, tc := range map[string]struct{ manifest, want string }{
		"empty":                 {"", "empty"},
		"a second document":     {check("  - {name: a, kind: pure, shell: x}\n") + "---\nversion: 1\n", "more than one YAML document"},
		"an unknown field":      {check("  - {name: a, kind: pure, shell: x, retries: 3}\n"), "retries"},
		"a duplicate key":       {"version: 1\nversion: 1\n", "already defined"},
		"a version":             {"version: 2\nchecks: []\n", "version 2"},
		"no checks":             {"version: 1\ndefaults:\n  timeout: 1m\n", "no checks"},
		"no kind":               {check("  - {name: a, shell: x}\n"), "no kind"},
		"an unknown kind":       {check("  - {name: a, kind: gpu, shell: x}\n"), `kind "gpu"`},
		"no name":               {check("  - {kind: pure, shell: x}\n"), "no name"},
		"a bad name":            {check("  - {name: 'A b', kind: pure, shell: x}\n"), "not a check name"},
		"a name twice":          {check("  - {name: a, kind: pure, shell: x}\n  - {name: a, kind: pure, shell: y}\n"), "declared twice"},
		"command and shell":     {check("  - {name: a, kind: pure, shell: x, command: [y]}\n"), "both command and shell"},
		"nothing to run":        {check("  - {name: a, kind: pure}\n"), "nothing to run"},
		"an empty program":      {check("  - {name: a, kind: pure, command: ['']}\n"), "program, is empty"},
		"image and dir":         {check("  - {name: a, kind: pure, shell: x, dir: d, image: 'a@sha256:" + digest + "'}\n"), "both image and dir"},
		"an image by tag":       {check("  - {name: a, kind: pure, shell: x, image: 'postgres:16'}\n"), "not pinned by digest"},
		"network without image": {check("  - {name: a, kind: pure, shell: x, network: n}\n"), "names no image"},
		"no timeout":            {"version: 1\nchecks:\n  - {name: a, kind: pure, shell: x}\n", "no timeout"},
		"a negative timeout":    {check("  - {name: a, kind: pure, shell: x, timeout: -1m}\n"), "longer than zero"},
		"a timeout too long":    {check("  - {name: a, kind: pure, shell: x, timeout: 25h}\n"), "over the 24h"},
		"not a duration":        {check("  - {name: a, kind: pure, shell: x, timeout: soon}\n"), "not a duration"},
		"a skip on timeout":     {check("  - {name: a, kind: pure, shell: x, skip_exit: [124]}\n"), "ran out of time"},
		"a skip on zero":        {check("  - {name: a, kind: pure, shell: x, skip_exit: [0]}\n"), "1 to 255"},
		"a parallelism":         {"version: 1\ndefaults:\n  timeout: 1m\n  parallelism: 17\nchecks:\n  - {name: a, kind: pure, shell: x}\n", "parallelism 17"},
		"a kind twice":          {"version: 1\nkinds: [{name: pure}]\ndefaults:\n  timeout: 1m\nchecks:\n  - {name: a, kind: pure, shell: x}\n", "declared twice, or is built in"},
		"a bad command name":    {check("  - {name: a, kind: pure, shell: x, requires: {commands: ['ls; rm']}}\n"), "not a command name"},
		"a bad env name":        {check("  - {name: a, kind: pure, shell: x, requires: {env: ['A-B']}}\n"), "not an environment variable"},
		"an os":                 {check("  - {name: a, kind: pure, shell: x, requires: {os: windows}}\n"), "requires.os"},
		"a tcp endpoint":        {check("  - {name: a, kind: pure, shell: x, requires: {tcp: ['db']}}\n"), "not a host:port"},
		"a tcp port":            {check("  - {name: a, kind: pure, shell: x, requires: {tcp: ['db:0']}}\n"), "no port from 1"},
		"a NUL":                 {check("  - {name: a, kind: pure, shell: \"x\\0y\"}\n"), "NUL"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.manifest))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

// TestParse_RefusesAnOversizedManifest refuses past MaxManifestBytes
// before decoding anything.
func TestParse_RefusesAnOversizedManifest(t *testing.T) {
	if _, err := Parse(make([]byte, MaxManifestBytes+1)); err == nil || !strings.Contains(err.Error(), "over the") {
		t.Fatalf("Parse = %v", err)
	}
}
