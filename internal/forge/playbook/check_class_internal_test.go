// The cross-check between a converted task's class and its method's check
// support (Phase 46, consuming Phase 35's classifier).
//
// Two declarations answer one question from two sides. The module table
// says what kind of change a call makes (Call.Class: observe, asserted,
// imperative), and each method says whether it can predict its own change
// (Manifest.SupportsCheck, and Descriptor.CheckCall when only some calls
// can be). Neither is inferred from the other, on purpose, and so they can
// disagree; this test is what notices when they do.
//
// What agreement means: an observe or asserted call is expected to be
// checkable, at least for some calls, since reading state or naming a
// desired state is what a prediction needs; an imperative call is expected
// not to be, since its effect is not known without doing it. A
// disagreement in EITHER direction fails unless it is listed below with
// the reason a person accepted it, and a listed pair that no longer
// disagrees fails too. Either side could be the wrong one, so neither is
// allowed to change silently.
package playbook

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// reviewedCheckClass lists every table call whose class and check support
// disagree, keyed "module -> method", each with the reason it stands. Each
// reason was checked against the method's own Check and doc comment when
// the pair was listed (2026-09-27), not inferred from its name.
var reviewedCheckClass = map[string]string{
	// Imperative calls whose check is exact, because the real run always
	// makes the change: the prediction is "changed", which is the truth.
	"ansible.builtin.service -> svc.restart": "dispatches to the concrete manager's restart, and each one's " +
		"check always predicts the restart a real run always performs",
	"ansible.builtin.systemd_service -> svc.systemd.restart": "CheckRestart always predicts a change, because " +
		"a restart is never converged; it reads the unit and refuses a masked or unknown one as the run does",
	"ansible.builtin.systemd_service -> svc.systemd.daemon_reload": "CheckDaemonReload predicts the reload " +
		"a real run always reports, since systemd cannot say whether a reload would differ",
	"ansible.windows.win_service -> svc.windows.restart": "CheckRestart reads the service with Get-Service, " +
		"refuses as the run does, and predicts the restart the run always performs",
	"ansible.builtin.file -> file.touch": "a touch always moves the modification time, so its check always " +
		"predicts a change, with remotefile.PredictTouch as the after half",

	// Imperative calls checkable only when a guard decides them, which the
	// method says per call through CheckCall and the report says through
	// TaskResult.CanCheck.
	"ansible.builtin.command -> exec.command": "checkable only with creates or removes, which the table " +
		"carries over, so the guard alone decides whether the command would run",
	"ansible.builtin.shell -> exec.shell": "checkable only with creates or removes, as exec.command",
	"ansible.builtin.raw -> exec.shell": "raw takes no creates or removes, so exec.shell's CheckCall refuses " +
		"every call raw converts to, and each is reported unchecked; the method is partly checkable, the calls are not",

	// Calls whose class expects a check the method cannot give. Doubt ends
	// as unchecked, never as a guess.
	"ansible.builtin.wait_for -> pleiades.builtin.wait.port": "it only reads, but what it waits for is " +
		"usually an earlier task's change, which a check never makes, so a check would time out where the run succeeds",
	"ansible.netcommon.netconf_config -> net.netconf.config": "a desired state, but only applying the document " +
		"says what the device makes of it, and a candidate-datastore check would stage it on the device",
}

// checkSupport reports whether a method can check every call (full) or
// only the calls its CheckCall accepts (partial).
func checkSupport(fqcn string) (full, partial bool) {
	d, ok := collection.Lookup(fqcn)
	if !ok || !d.Manifest.SupportsCheck {
		return false, false
	}
	if d.CheckCall != nil {
		return false, true
	}
	return true, false
}

// TestCheckSupportAgreesWithTheConversionClass walks every call the module
// tables can make and holds its class against its method's check support.
func TestCheckSupportAgreesWithTheConversionClass(t *testing.T) {
	seen := map[string]bool{}
	var disagreements []string
	for _, e := range Entries() {
		for _, c := range entryCalls(e) {
			key := e.Module + " -> " + c.FQCN
			if seen[key] {
				continue
			}
			seen[key] = true

			full, partial := checkSupport(c.FQCN)
			checkable := full || partial
			var agrees bool
			switch c.Class {
			case ClassObserve, ClassAsserted:
				agrees = checkable
			case ClassImperative:
				agrees = !checkable
			default:
				// The table never assigns computed (that class is set only on
				// a blocked placeholder) and never leaves a call unclassified.
				t.Errorf("%s declares class %s, which a table call never carries", key, c.Class)
				continue
			}
			if agrees {
				continue
			}
			disagreements = append(disagreements, key)
			if _, reviewed := reviewedCheckClass[key]; !reviewed {
				t.Errorf("%s is classed %s but its method %s; either the class or the method's check support "+
					"is wrong, or the pair belongs in reviewedCheckClass with the reason it stands",
					key, c.Class, describeCheck(full, partial))
			}
		}
	}

	for key := range reviewedCheckClass {
		switch {
		case !seen[key]:
			t.Errorf("reviewedCheckClass lists %s, which no table call makes any more", key)
		case !slices.Contains(disagreements, key):
			t.Errorf("reviewedCheckClass lists %s, whose class and check support now agree; remove it", key)
		}
	}
}

// describeCheck says what a method's check support is, for a message.
func describeCheck(full, partial bool) string {
	switch {
	case full:
		return "checks every call"
	case partial:
		return "checks some calls (CheckCall)"
	default:
		return "cannot be checked"
	}
}
