// Fuzzing every probe's parser, on every platform facts.gather reads (Phase
// 41c): whatever a device prints, a parser returns facts without
// panicking, and a family's keys are its prefix followed by a name holding
// no space, slash or unprintable character, which itemKey percent-encodes.
package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

func FuzzGatherParsers(f *testing.F) {
	seeds := []string{
		"", "\n", "ii\tbash\t5.2\tamd64\n", "bash\t5.2-1.el9\tx86_64\n", "musl 1.2.5-r0 x86_64 {musl} (MIT) [installed]\n",
		"sshd.service loaded active running OpenSSH\n" + serviceSectionMark + "\nsshd.service enabled\n",
		"brotli\t1.2.1,1\tFreeBSD:15:amd64\n", "DAEMON\nsshd\n" + serviceSectionMark + "\n/etc/rc.d/sshd\n" + serviceSectionMark + "\nsshd\tsshd is running as pid 1.\n",
		"package-id: com.apple.pkg.A\nversion: 1\n", "\tservices = {\n\t\t 1 - \tcom.apple.x\n\t}\n" + serviceSectionMark + "\n",
		`{"host.nodename":"a b/c","hardware.memory_mb":1}`, `[{"name":"x y/z%","version":"1","arch":"amd64"}]`,
		`[{"name":"svc","state":"Running","mode":"Auto","delayed":true}]`, "{ sec = 1, usec = 2 }\n3\n", "total = 1.00M used",
		"name\x00with nul\t1\tamd64\n", "\xff\xfe bad utf-8\t1\tx\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	// Hostile outputs aimed at each parser of the files, ports and users
	// subsets (Phase 41c-2): extra marks, short lines, bad numbers, zones,
	// NULs, CRLF, invalid UTF-8 and JSON of the wrong shape.
	raw, err := os.ReadFile(filepath.Join("testdata", "parser-seeds.json"))
	if err != nil {
		f.Fatal(err)
	}
	var more []struct {
		Input string `json:"input"`
	}
	if err := json.Unmarshal(raw, &more); err != nil || len(more) == 0 {
		f.Fatalf("testdata/parser-seeds.json: %v (%d seeds)", err, len(more))
	}
	for _, s := range more {
		f.Add(s.Input)
	}
	var probes []gatherProbe
	for _, platform := range []string{platformLinux, platformFreeBSD, platformDarwin, platformWindows} {
		probes = append(probes, gatherProbesFor(platform)...)
	}
	f.Fuzz(func(t *testing.T, out string) {
		for _, p := range probes {
			// A family's answer is judged before it is parsed: whatever the
			// device printed, the judgment does not panic, and a body it
			// keeps holds neither mark (gather_outcome.go).
			if p.family != "" && p.commandFor == nil {
				if outcome, body, _ := readOutcome(out, p.check); outcome == outcomeRead {
					for _, line := range strings.Split(body, "\n") {
						if l := strings.TrimSpace(line); l == gatherAbsentMark || l == gatherFailedMark {
							t.Fatalf("a kept answer still holds a mark: %q", body)
						}
					}
				}
			}
			facts := p.parse(out)
			if p.commandFor != nil {
				// A probe built from paths is keyed by them as Gather keys it.
				facts = filesFacts([]string{"/a b", `C:\x\y`}, facts)
			}
			for _, fact := range facts {
				if p.family == "" {
					// Gather emits only the keys a single probe declares
					// (gatherWanted), whatever its parser read.
					continue
				}
				name, ok := strings.CutPrefix(fact.key, p.family)
				if !ok || name == "" {
					t.Fatalf("%s answered %q outside its family %s", p.name(), fact.key, p.family)
				}
				for _, r := range name {
					if r == '/' || unicode.IsSpace(r) || !unicode.IsPrint(r) {
						t.Fatalf("%s answered %q, whose name holds %q", p.name(), fact.key, r)
					}
				}
				if v, ok := fact.value.(map[string]any); ok {
					// A mode is four octal digits; a port 1 to 65535.
					if m, ok := v["mode"].(string); ok && len(m) != 4 {
						t.Fatalf("%s answered mode %q", p.name(), m)
					}
					if n, ok := v["port"].(int); ok && (n < 1 || n > 65535) {
						t.Fatalf("%s answered port %d", p.name(), n)
					}
				}
				if _, ok := fact.value.(map[string]any); !ok {
					t.Fatalf("%s answered %q with %T, not an object", p.name(), fact.key, fact.value)
				}
			}
		}
	})
}
