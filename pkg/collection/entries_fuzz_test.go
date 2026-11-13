// Fuzz tests of the checks Phase 127b added to a manifest: a structured
// parameter's entries and a method's alternatives.
package collection_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// FuzzEntryProblems: any JSON value given to a declared list of maps never
// panics the check, every problem names the parameter by place, and a value
// whose every entry holds only declared keys of the declared type, with a
// reason, has none.
func FuzzEntryProblems(f *testing.F) {
	f.Add(`[{"text":"ip ospf authentication-key","reason":"lab only","not on a real network":null}]`)
	f.Add(`[{"text":"bfd"}]`)
	f.Add(`[{"feature":7,"reason":["x"]}]`)
	f.Add(`[[{"text":"x"}],"{{ vars.x }}",{"reason":"{{ vars.y }}"}]`)
	f.Add(`{"text":"x"}`)
	f.Fuzz(func(t *testing.T, raw string) {
		var value any
		if json.Unmarshal([]byte(raw), &value) != nil {
			return
		}
		p := acceptParam()
		for _, problem := range collection.EntryProblems(p, value) {
			if !strings.HasPrefix(problem, "accept_unrecognized[") {
				t.Fatalf("a problem not naming its place: %q", problem)
			}
		}
		list, ok := value.([]any)
		if !ok {
			return
		}
		clean := true
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			_, hasReason := m["reason"]
			clean = clean && hasReason
			for k, v := range m {
				_, isText := v.(string)
				clean = clean && (k == "text" || k == "feature" || k == "reason") && (isText || v == nil)
			}
		}
		if got := collection.EntryProblems(p, value); clean && len(got) != 0 {
			t.Fatalf("a clean value %s was refused: %q", raw, got)
		}
	})
}

// FuzzAdmitsAlternatives: for any set of capabilities, Admits over a
// manifest with alternatives admits a device exactly when ChooseAlternative
// chooses one of them and the device reaches one of the manifest's
// transports, and the alternative chosen is one the device fits whole.
func FuzzAdmitsAlternatives(f *testing.F) {
	f.Add(uint16(0))
	f.Add(uint16(0b111))
	f.Add(uint16(0b1011))
	all := []capability.Name{capability.NameFRR, capability.NameCommandExec, capability.NameSSHTransport, capability.NameCiscoIOS,
		capability.NameTelnet, capability.NameConsoleServed, capability.NameLinux, capability.NameWinRM}
	m := routerManifest()
	f.Fuzz(func(t *testing.T, bits uint16) {
		var caps []capability.Name
		for i, c := range all {
			if bits&(1<<i) != 0 {
				caps = append(caps, c)
			}
		}
		dev := stubWith(caps...)
		admitted := collection.Admits(dev, "wait.routes", m)
		alt, chooseErr := collection.ChooseAlternative(dev, "wait.routes", m)
		reached := collection.CheckTransports(dev, "wait.routes", m) == nil
		if (admitted == nil) != (chooseErr == nil && reached) {
			t.Fatalf("%v: Admits %v, ChooseAlternative %q %v, reached %t", caps, admitted, alt.Name, chooseErr, reached)
		}
		if chooseErr != nil {
			return
		}
		for _, c := range alt.RequiredCapabilities {
			if !dev.HasCapability(c) {
				t.Fatalf("%v: chose %q, whose %s the device lacks", caps, alt.Name, c)
			}
		}
		if collection.CheckTransports(dev, "wait.routes", collection.Manifest{SupportedTransports: alt.SupportedTransports}) != nil {
			t.Fatalf("%v: chose %q, none of whose transports the device reaches", caps, alt.Name)
		}
	})
}
