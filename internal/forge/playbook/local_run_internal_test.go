// Tests for localRun: a task written to run on the Ansible controller
// converts only when every native method it can become runs in the host
// process (Phase 117a, PLAN.md Section 14's execution context).
package playbook

import (
	"slices"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog" // registers the real built-in methods
)

// TestLocalRun: a controller-side method satisfies delegate_to: localhost,
// which is dropped with a finding; a device-side method blocks it, naming
// the method; and an entry that can become either blocks, since one of its
// calls would run on the device. No table entry maps to a controller-side
// method yet, so the entries here are built for the test from real methods.
func TestLocalRun(t *testing.T) {
	controller := &Entry{Module: "test.local_api", Default: &Call{FQCN: "http.request"}}
	device := &Entry{Module: "test.device", Default: &Call{FQCN: "exec.command"}}
	mixed := &Entry{Module: "test.mixed", Default: &Call{FQCN: "http.request"},
		Selectors: []Selector{{Arg: "state", Choices: []Choice{{Values: []string{"present"}, Call: &Call{FQCN: "exec.command"}}}}}}
	for _, tc := range []struct {
		name        string
		entry       *Entry
		mark        localMark
		wantBlocked bool
		wantCode    Code
	}{
		{"controller-side method", controller, localMark{key: "delegate_to: localhost"}, false, "keyword.local_satisfied"},
		{"device-side method", device, localMark{key: "delegate_to: localhost"}, true, "keyword.delegate_to"},
		{"local_action on a device-side method", device, localMark{key: "local_action"}, true, "keyword.local_action"},
		{"a call that may run on the device", mixed, localMark{key: "connection: local"}, true, "keyword.local_action"},
		{"no mark at all", device, localMark{}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := &translator{file: "pb.yml", ix: newVarIndex(), seen: map[findingKey]string{}, registers: map[string]produced{}}
			spec := &leafSpec{name: "t", local: tc.mark}
			if got := tr.localRun(tc.entry, spec); got != tc.wantBlocked {
				t.Fatalf("localRun() blocked = %v, want %v", got, tc.wantBlocked)
			}
			var codes []Code
			for _, f := range tr.findings {
				codes = append(codes, f.Code)
			}
			if tc.wantCode != "" && !slices.Contains(codes, tc.wantCode) {
				t.Errorf("findings %v lack %s", codes, tc.wantCode)
			}
			if tc.wantCode == "" && len(codes) != 0 {
				t.Errorf("an unmarked task raised %v", codes)
			}
		})
	}
}
