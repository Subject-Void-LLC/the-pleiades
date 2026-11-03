// Edge cases of facts.gather's plan and parsers that the platform tests do
// not reach: a refused subset or pattern meeting a family first, a filter
// matching a family through a pattern that is not its prefix, and the lines
// each lister skips.
package facts

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

func TestGatherPlan_FamiliesInTheRefusalsAndTheFilter(t *testing.T) {
	if _, err := gatherPlan(map[string]any{gatherParamSubset: []any{"bogus"}}); err == nil {
		t.Error("an unknown subset was planned")
	}
	// The first probe a packages-only plan meets is a family, which reads
	// the malformed pattern first.
	if _, err := gatherPlan(map[string]any{gatherParamSubset: []any{"packages", "!min"}, gatherParamFilter: []any{"["}}); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Errorf("a malformed pattern meeting a family: %v", err)
	}
	// A filter wanting nothing of the chosen subsets names the families it
	// could have matched.
	_, err := gatherPlan(map[string]any{gatherParamSubset: []any{"packages", "services", "!min"}, gatherParamFilter: []any{"ansible_*"}})
	if err == nil || !strings.Contains(err.Error(), "package.*") || !strings.Contains(err.Error(), "service.*") {
		t.Errorf("refusal = %v", err)
	}
	// * is no family's prefix, and still wants every family's members.
	for pattern, want := range map[string]bool{"*": true, "ansible_*": false, "service.*": false} {
		if got, err := gatherFamilyWanted([]string{pattern}, gatherFamilyPackage); err != nil || got != want {
			t.Errorf("%q wants packages: %v, %v", pattern, got, err)
		}
	}
}

func TestGatherListers_SkipWhatIsNotAnItem(t *testing.T) {
	rpm := parseRPM("gpg-pubkey\t(none)\t1-2\t(none)\nlibgcc\t(none)\t11.4-1\t(none)\nshort\tline\n")
	if !reflect.DeepEqual(rpm, []installedPackage{{name: "libgcc", version: "11.4-1", source: sourceRPM}}) {
		t.Errorf("rpm = %v", rpm)
	}
	apk := parseAPK("-1.0-r0 x86_64 {x} (MIT) [installed]\nmusl-1.2.5-r0 x86_64 {musl} (MIT) [installed]\n")
	if len(apk) != 1 || apk[0].name != "musl" {
		t.Errorf("apk = %v", apk)
	}
	launchd := valuesOf(parseLaunchd("\tservices = {\n\t\t5 x\n\t\t7 - a.b\n\t}\n" + serviceSectionMark + "\n"))
	if len(launchd) != 1 || launchd["service.a.b"] == nil {
		t.Errorf("launchd = %v", launchd)
	}
	if got := valuesOf(parseWindowsObject(`{"ansible_flag":true,"ansible_system":"Win32NT"}`)); got["ansible_flag"] != true {
		t.Errorf("a boolean = %v", got)
	}
	if got := (gatherProbe{command: "cat /proc/uptime"}).name(); got != "cat /proc/uptime" {
		t.Errorf("an SSH probe is named %q", got)
	}
}

// clashContext hands over a certificate bundle and a password together,
// which WinRM authentication refuses.
type clashContext struct{ winTestContext }

func (clashContext) InjectSecrets() map[string]string {
	return map[string]string{"pfx_base64": "MA==", "password": "secret"}
}

func TestGather_AnUnusableWinRMCredentialIsRefusedBeforeContact(t *testing.T) {
	asked := answerWindows(t, map[string]winrmexec.Result{})
	_, err := Gather(context.Background(), &clashContext{winTestContext{facts: map[string]any{}}}, newWinTestHost(), nil)
	if err == nil || !strings.HasPrefix(err.Error(), "facts.gather: ") || len(*asked) != 0 {
		t.Errorf("err = %v, asked %v", err, *asked)
	}
}
