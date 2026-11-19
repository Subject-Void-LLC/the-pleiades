// Package hostfacts_test: systemd's service facts, which the gatherer and the
// methods that read a unit back must make identically.
package hostfacts_test

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/hostfacts"
)

func TestParseSystemd_MakesOneFactPerServiceLeavingOutSessions(t *testing.T) {
	mark := hostfacts.ServiceSectionMark
	out := "sshd.service enabled enabled\nnginx.service disabled enabled\nfoo@.service static -\nrun-x.service transient -\n" + mark + "\n" +
		"sshd.service loaded active running OpenSSH\n● nginx.service loaded failed failed nginx\nuser@1000.service loaded active running u\nrun-x.service loaded active running x\n" + mark + "\n"
	got := map[string]any{}
	for _, f := range hostfacts.ParseSystemd(out) {
		got[f.Key] = f.Value
	}
	want := map[string]any{
		"service.sshd.service":  map[string]any{"state": "running", "enabled": true, "status": "enabled", "source": "systemd"},
		"service.nginx.service": map[string]any{"state": "failed", "enabled": false, "status": "disabled", "source": "systemd"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseSystemd = %v\nwant %v", got, want)
	}
	if hostfacts.ParseSystemd("no mark") != nil {
		t.Error("output without its sections was parsed")
	}
}

func TestCheckSystemd_RefusesAnAnswerWithNoLoadedService(t *testing.T) {
	mark := hostfacts.ServiceSectionMark
	if err := hostfacts.CheckSystemd("a\n" + mark + "\n" + mark + "\n"); err == nil {
		t.Error("a listing naming no loaded service was accepted")
	}
	if err := hostfacts.CheckSystemd("only one section"); err == nil {
		t.Error("an answer without its three sections was accepted")
	}
	if err := hostfacts.CheckSystemd("x\n" + mark + "\nsshd.service loaded active running s\n" + mark + "\n"); err != nil {
		t.Errorf("a whole answer was refused: %v", err)
	}
}

func TestSessionUnit_LeavesOutWhatLastsOnlyAsLongAsAConnection(t *testing.T) {
	sockets := map[string]bool{"sshd.socket": true}
	for name, want := range map[string]bool{
		"sshd@3-10.0.0.1:22-10.0.0.9:5000.service": true,
		"user@1000.service":                        true,
		"getty@tty1.service":                       false,
		"sshd.service":                             false,
	} {
		if got := hostfacts.SessionUnit(name, "static", sockets); got != want {
			t.Errorf("SessionUnit(%s) = %v, want %v", name, got, want)
		}
	}
	if !hostfacts.SessionUnit("run-x.service", "transient", nil) {
		t.Error("a transient unit was kept")
	}
}

func TestSplitServiceState_TakesTheStateIntoItsOwnFact(t *testing.T) {
	got := hostfacts.SplitServiceState(hostfacts.Fact{Key: "service.sshd.service", Value: map[string]any{"state": "running", "enabled": true}})
	want := []hostfacts.Fact{
		{Key: "service.sshd.service", Value: map[string]any{"enabled": true}},
		{Key: "service_state.sshd.service", Value: "running"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SplitServiceState = %+v, want %+v", got, want)
	}
	other := hostfacts.Fact{Key: "package.curl", Value: map[string]any{"version": "1"}}
	if got := hostfacts.SplitServiceState(other); !reflect.DeepEqual(got, []hostfacts.Fact{other}) {
		t.Fatalf("a fact of another family was changed: %+v", got)
	}
}

func TestServiceFactSpecs_DeclareTheGatherersKinds(t *testing.T) {
	specs := hostfacts.ServiceFactSpecs()
	if len(specs) != 2 || specs[0].Key != hostfacts.ServiceFamily || specs[1].Key != hostfacts.ServiceStateFamily {
		t.Fatalf("specs = %+v", specs)
	}
	if specs[0].Kind != "stable" || specs[1].Kind != "volatile" {
		t.Fatalf("kinds = %s, %s; want stable and volatile", specs[0].Kind, specs[1].Kind)
	}
}
