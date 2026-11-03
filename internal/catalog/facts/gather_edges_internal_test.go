// Edge cases of facts.gather's plan and parsers that the platform tests do
// not reach: a refused subset or pattern meeting a family first, a filter
// matching a family through a pattern that is not its prefix, and the lines
// each lister skips.
package facts

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
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
	// A family is wanted by a pattern whose literal start agrees with its
	// prefix, however that pattern begins.
	for pattern, want := range map[string]bool{"*": true, "*nginx": true, "pack*": true, "package.nginx": true, "package.ng?nx": true,
		"ansible_*": false, "service.*": false, "pkg*": false} {
		if got := gatherFamilyWanted([]string{pattern}, gatherFamilyPackage); got != want {
			t.Errorf("%q wants packages: %v", pattern, got)
		}
	}
	// A malformed pattern is refused whichever probe it would meet first,
	// one starting like a family's included.
	for _, bad := range []string{"[", "package.[", "ansible_[x"} {
		if _, err := gatherPlan(map[string]any{gatherParamSubset: []any{"all"}, gatherParamFilter: []any{bad}}); err == nil || !strings.Contains(err.Error(), "malformed") {
			t.Errorf("%q: %v", bad, err)
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

func TestGather_MoreFactsThanOneTaskMayRecordFailsTheTask(t *testing.T) {
	var many strings.Builder
	many.WriteString("[")
	for i := range collection.MaxFactsPerObservation {
		if i > 0 {
			many.WriteString(",")
		}
		fmt.Fprintf(&many, `{"name":"p%d","version":"1","arch":"amd64"}`, i)
	}
	many.WriteString("]")
	answerWindows(t, map[string]winrmexec.Result{
		gatherSubsetMin:      {Stdout: `{"ansible_system":"Win32NT"}`},
		gatherSubsetPackages: {Stdout: many.String()},
	})
	rc := &winTestContext{facts: map[string]any{}}
	_, err := Gather(context.Background(), rc, newWinTestHost(), map[string]any{gatherParamSubset: []any{"min", "packages"}})
	if err == nil || !strings.Contains(err.Error(), "more than the 8192 facts") {
		t.Fatalf("err = %v", err)
	}
	// The probe that crossed the cap emitted nothing; the one before it did.
	if len(rc.facts) != 1 {
		t.Errorf("%d facts emitted, want only the min probe's", len(rc.facts))
	}
}

func TestLastLine_SkipsALoginBanner(t *testing.T) {
	for out, want := range map[string]string{"FreeBSD\n": "FreeBSD", "Welcome to the lab\n\nLinux\n\n": "Linux", "": ""} {
		if got := lastLine(out); got != want {
			t.Errorf("lastLine(%q) = %q, want %q", out, got, want)
		}
	}
}

func TestProbes_AreTheProbesGatherRuns(t *testing.T) {
	rpm := Probes("Linux", gatherSubsetPackages, "rpm -qa")
	if len(rpm) != 1 || len(Probes("Linux", gatherSubsetPackages, "")) != len(linuxPackageProbes()) || Probes("Linux", "nosuch", "") != nil {
		t.Fatalf("Probes = %v", rpm)
	}
	got := rpm[0].Read("bash\t(none)\t5.2-1\tx86_64\nbash\t(none)\t5.2-1\ti686\n")
	if len(got) != 2 || got["package.bash"] == nil || got["package.bash:i686"] == nil {
		t.Errorf("Read = %v", got)
	}
}
