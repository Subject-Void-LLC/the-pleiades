// Fuzzing every probe's parser, on every platform facts.gather reads (Phase
// 41c): whatever a device prints, a parser returns facts without
// panicking, and a family's keys are its prefix followed by a name holding
// no space, slash or unprintable character, which itemKey percent-encodes.
package facts

import (
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
		`{"ansible_nodename":"a b/c","ansible_memtotal_mb":1}`, `[{"name":"x y/z%","version":"1","arch":"amd64"}]`,
		`[{"name":"svc","state":"Running","mode":"Auto","delayed":true}]`, "{ sec = 1, usec = 2 }\n3\n", "total = 1.00M used",
		"name\x00with nul\t1\tamd64\n", "\xff\xfe bad utf-8\t1\tx\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	var probes []gatherProbe
	for _, platform := range []string{platformLinux, platformFreeBSD, platformDarwin, platformWindows} {
		probes = append(probes, gatherProbesFor(platform)...)
	}
	f.Fuzz(func(t *testing.T, out string) {
		for _, p := range probes {
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
				if _, ok := fact.value.(map[string]any); !ok {
					t.Fatalf("%s answered %q with %T, not an object", p.name(), fact.key, fact.value)
				}
			}
		}
	})
}
