// Architecture rule for absence (Phase 131, R4), the guard half: every
// implemented, read-only built-in method either declares an exists return
// (which TestALookupAnswersAbsenceAsData then holds to the whole
// convention) or is named below with the reason it answers no exists. The
// audit of 2026-10-28 judged the 56 read-only methods of that day; this is
// what keeps the 57th from arriving unjudged.
package archtest

import (
	"sort"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// Reasons a read-only method answers no exists, shared by the methods they
// fit. Each says what the method answers instead when the thing it is asked
// about is not there.
const (
	whyLists  = "it lists; when there is nothing, an empty list is the answer"
	whyDevice = "it describes the device itself; a device not there is unreachable, not absent"
	whyGate   = "it is a gate: failing on what it asserts is not so is its purpose"
	whyWait   = "it is a wait: failing at its timeout is its purpose"
	whyProbe  = "it is a probe: its result is the answer, a lost reply included"
)

// noExistsReasons names every implemented, read-only built-in method with
// no exists return, and why. A method added here needs a reason a reviewer
// can check against its code; a lookup of one named thing that can be
// absent declares exists instead (TestALookupAnswersAbsenceAsData).
var noExistsReasons = map[string]string{
	"ai.model.list":                     whyLists,
	"ai.prompt.verify":                  whyGate + " (it verifies the signed document the task names)",
	"ai.provider.list":                  whyLists,
	"build.digests.check":               whyGate,
	"build.lock.verify":                 whyGate,
	"container.engine.info":             whyDevice,
	"container.image.info":              "a named image not there is listed under missing, never an error, in a run and a check (TestInfo_AMissingImageIsListedInBothModes)",
	"container.image.search":            "it searches; an empty results list is the answer",
	"container.list":                    whyLists,
	"container.registry.info":           "a registry with no login is logged_in false, and a login file not there reads as no logins (TestLogoutAndInfo_TheFileAsData)",
	"edge.access":                       whyGate + " (one application not there fails naming it, run and check alike; TestEdge_AGateFailsOnWhatIsNotThere)",
	"edge.dns":                          "a name with no records is an empty records list, which expect.absent asserts; a zone not there fails, as the scope of the read",
	"edge.tunnel":                       whyGate + " (one tunnel not there fails naming it, run and check alike; TestEdge_AGateFailsOnWhatIsNotThere)",
	"facts.gather":                      whyDevice,
	"gate.list":                         "it lists the manifest's checks, and the manifest is the task's own input",
	"http.check":                        whyGate + " (a 404 is data when status_code names it)",
	"http.wait":                         whyWait,
	"net.batfish.check":                 whyGate,
	"net.catalyst.device_facts":         whyLists + " (every device the controller manages; no name is asked)",
	"net.catalyst.reachability":         whyLists,
	"net.catalyst.site_facts":           whyLists + " (every site; no name is asked)",
	"net.catalyst.tag_facts":            whyLists + " (every tag; no name is asked)",
	"net.frr.command":                   "it runs a show command, and the command's output is the answer",
	"net.frr.facts":                     whyDevice,
	"net.ios.facts":                     whyDevice,
	"net.ios.parse":                     "it parses a show command's output; output with nothing found is empty: true",
	"net.ios.ping":                      whyProbe,
	"net.pcap.summary":                  "it summarizes the capture file the task names; a file not there is refused input, not a lookup",
	"net.ssh.ping":                      whyProbe,
	"net.trace":                         whyProbe,
	"pleiades.builtin.connection.reset": "it looks nothing up",
	"pleiades.builtin.wait.port":        whyWait,
	"scan.secrets":                      whyGate,
	"scan.vuln":                         whyGate,
	"virt.clab.edgeshark.status":        whyDevice + " (it reads the server's Edgeshark status, whose running is false when it does not run)",
	"virt.clab.image.list":              whyLists,
	"virt.clab.lab.list":                whyLists,
	"virt.clab.server.health":           whyDevice,
	"virt.clab.server.metrics":          whyDevice,
	"virt.clab.server.version":          whyDevice,
	"virt.clab.server.version_check":    whyDevice + " (it asks whether a newer containerlab exists, which is an answer either way)",
	"virt.clab.ssh_session.list":        whyLists,
	"virt.clab.topology.generate":       "it asks the server to build a topology from the task's own description of its tiers, and looks nothing up",
	"virt.cml.lab.list":                 whyLists,
	"virt.vbox.vm.list":                 whyLists,
	"wait.connection":                   whyWait,
	"wait.path":                         whyWait,
	"wait.routes":                       whyWait,
	"wait.search":                       whyWait,
	"win.cpu.topology":                  whyDevice,
}

// TestEveryReadOnlyMethodDeclaresExistsOrAReason holds every implemented,
// read-only built-in method to declaring an exists return or having an
// entry in noExistsReasons, and every entry there to naming such a method
// that declares none, so the table never outlives what it judged.
func TestEveryReadOnlyMethodDeclaresExistsOrAReason(t *testing.T) {
	var methods []collection.Descriptor
	for _, cfg := range catalogdata.Collections {
		if desc, ok := collection.Lookup(cfg.Name); ok {
			methods = append(methods, desc)
		}
	}
	readOnly, declare := 0, 0
	for _, desc := range methods {
		if desc.Manifest.Status != collection.StatusImplemented || !desc.Manifest.Reversibility.ReadOnly {
			continue
		}
		readOnly++
		if _, ok := returnNamed(desc.Manifest.Doc, existsReturn); ok {
			declare++
		}
	}
	if readOnly == 0 || declare == 0 {
		t.Fatalf("%d read-only methods, %d declaring exists: this test proved nothing", readOnly, declare)
	}
	for _, problem := range existsOrReasonProblems(methods, noExistsReasons) {
		t.Error(problem + " (Phase 131, R4)")
	}
	t.Logf("%d implemented read-only methods: %d declare exists, %d have a reason", readOnly, declare, len(noExistsReasons))
}

// existsOrReasonProblems returns, in name order, each implemented
// read-only method of methods that declares no exists return and has no
// reason, each that declares one and also has a reason, and each reason
// naming no implemented read-only method of methods.
func existsOrReasonProblems(methods []collection.Descriptor, reasons map[string]string) []string {
	var problems []string
	judged := map[string]bool{}
	for _, desc := range methods {
		if desc.Manifest.Status != collection.StatusImplemented || !desc.Manifest.Reversibility.ReadOnly {
			continue
		}
		judged[desc.Name] = true
		_, declares := returnNamed(desc.Manifest.Doc, existsReturn)
		reason, listed := reasons[desc.Name]
		switch {
		case declares && listed:
			problems = append(problems, desc.Name+" declares exists and also has a reason for not declaring it; remove its noExistsReasons entry")
		case !declares && !listed:
			problems = append(problems, desc.Name+" is read-only and declares no exists return: declare exists (bool, returned always) if it can be asked about something absent, else add it to noExistsReasons with why it answers none")
		case listed && reason == "":
			problems = append(problems, desc.Name+" has an empty reason in noExistsReasons")
		}
	}
	for name := range reasons {
		if !judged[name] {
			problems = append(problems, name+" is in noExistsReasons but is not an implemented read-only method; remove the entry")
		}
	}
	sort.Strings(problems)
	return problems
}

// TestExistsOrReasonRule_Control proves the rule above sees what it looks
// for: a read-only method with neither an exists return nor a reason is
// named, as are one with both, an empty reason and a reason for a method
// that is not read-only, while a method with exactly one of the two, and a
// method that is not read-only with neither, are not.
func TestExistsOrReasonRule_Control(t *testing.T) {
	method := func(name string, readOnly, exists bool) collection.Descriptor {
		d := collection.Descriptor{Name: name, Manifest: collection.Manifest{Status: collection.StatusImplemented}}
		d.Manifest.Reversibility.ReadOnly = readOnly
		if exists {
			d.Manifest.Doc.Returns = []collection.ReturnField{{Name: existsReturn, Type: "bool", Returned: "always"}}
		}
		return d
	}
	whole := []collection.Descriptor{
		method("x.thing.info", true, true),
		method("x.thing.list", true, false),
		method("x.thing.set", false, false),
	}
	if got := existsOrReasonProblems(whole, map[string]string{"x.thing.list": whyLists}); len(got) != 0 {
		t.Errorf("a whole catalog was named: %v", got)
	}
	for name, tc := range map[string]struct {
		methods []collection.Descriptor
		reasons map[string]string
	}{
		"neither":           {[]collection.Descriptor{method("x.new.info", true, false)}, map[string]string{}},
		"both":              {[]collection.Descriptor{method("x.thing.info", true, true)}, map[string]string{"x.thing.info": whyLists}},
		"an empty reason":   {[]collection.Descriptor{method("x.thing.list", true, false)}, map[string]string{"x.thing.list": ""}},
		"not read-only":     {[]collection.Descriptor{method("x.thing.set", false, false)}, map[string]string{"x.thing.set": whyLists}},
		"no longer there":   {nil, map[string]string{"x.gone.list": whyLists}},
		"declared, unbuilt": {[]collection.Descriptor{{Name: "x.later.list", Manifest: collection.Manifest{Status: collection.StatusDeclared, Reversibility: collection.Reversibility{ReadOnly: true}}}}, map[string]string{"x.later.list": whyLists}},
	} {
		if got := existsOrReasonProblems(tc.methods, tc.reasons); len(got) != 1 {
			t.Errorf("%s: problems %v, want exactly one", name, got)
		}
	}
}
