// Tests for Session.PodmanInfo against an engine answering libpod info the
// way Podman 5 does.
package containerengine

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// podmanInfoAnswer is a rootless Podman 5's libpod info, trimmed to what
// PodmanInfo reads plus a few fields it does not.
const podmanInfoAnswer = `{"host":{"arch":"amd64","os":"linux","kernel":"6.8.0-45-generic","hostname":"builder",
"cgroupVersion":"v2","cgroupManager":"systemd","cpus":8,"memTotal":16484372480,
"distribution":{"distribution":"fedora","version":"40"},"security":{"rootless":true,"seccompEnabled":true}},
"store":{"graphDriverName":"overlay","graphRoot":"/home/u/.local/share/containers/storage",
"containerStore":{"number":3,"paused":0,"running":1,"stopped":2},"imageStore":{"number":7}},
"version":{"APIVersion":"5.2.2","Version":"5.2.2","OsArch":"linux/amd64"}}`

// podmanSession is a Podman session whose engine answers every request
// with answer, through a guard allowing ops.
func podmanSession(kind capability.EngineKind, ops []Op, asked *string) (*Session, *pipeEngine) {
	engine := &pipeEngine{answer: func(req *http.Request) string {
		*asked = req.URL.Path
		return jsonAnswer("200 OK", podmanInfoAnswer)
	}}
	return &Session{Kind: kind, dial: guard(engine.dial, ops)}, engine
}

// TestPodmanInfo_ReadsLibpodInfo: the facts a runbook branches on come
// from libpod info's own fields.
func TestPodmanInfo_ReadsLibpodInfo(t *testing.T) {
	var asked string
	s, _ := podmanSession(capability.EnginePodman, PodmanInfoOps, &asked)
	info, err := s.PodmanInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if asked != "/v5.0.0/libpod/info" {
		t.Errorf("asked %s", asked)
	}
	if !info.Host.Security.Rootless || info.Host.CgroupVersion != "v2" || info.Store.GraphDriverName != "overlay" ||
		info.Version.APIVersion != "5.2.2" || info.Store.ContainerStore.Number != 3 || info.Store.ContainerStore.Running != 1 ||
		info.Store.ImageStore.Number != 7 || info.Host.Distribution.Distribution != "fedora" || info.Host.CPUs != 8 {
		t.Errorf("PodmanInfo = %+v", info)
	}
}

// TestPodmanInfo_RefusesWhatItWasNotOpenedFor: a Docker session is refused
// before contact, and a Podman session that did not declare the operation
// is refused by its guard.
func TestPodmanInfo_RefusesWhatItWasNotOpenedFor(t *testing.T) {
	var asked string
	s, engine := podmanSession(capability.EngineDocker, PodmanInfoOps, &asked)
	if _, err := s.PodmanInfo(context.Background()); err == nil || !strings.Contains(err.Error(), "Podman's") || engine.dials.Load() != 0 {
		t.Errorf("a Docker session = %v, %d dials", err, engine.dials.Load())
	}
	s, _ = podmanSession(capability.EnginePodman, []Op{OpInfo}, &asked)
	if _, err := s.PodmanInfo(context.Background()); err == nil || !strings.Contains(err.Error(), "not an operation this caller declared") {
		t.Errorf("an undeclared libpod info = %v", err)
	}
	if asked != "" {
		t.Errorf("the engine was asked %s", asked)
	}
}

// TestMethodManifest: the shared manifest is an engine method's, checkable
// and implemented, carrying what it is given.
func TestMethodManifest(t *testing.T) {
	m := MethodManifest(collection.Reversibility{Notes: "reads", ReadOnly: true}, collection.Doc{Summary: "s"})
	if !m.SupportsCheck || m.Status != collection.StatusImplemented || m.Doc.Summary != "s" || !m.Reversibility.ReadOnly ||
		len(m.RequiredCapabilities) != 1 || m.RequiredCapabilities[0] != capability.NameContainerEngine {
		t.Errorf("MethodManifest = %+v", m)
	}
	if !strings.HasPrefix(ReachNote, " The engine is reached") || HostKeyParam.Type != "bool" {
		t.Errorf("ReachNote %q, HostKeyParam %+v", ReachNote, HostKeyParam)
	}
}

// TestStringMapParam: text values only.
func TestStringMapParam(t *testing.T) {
	got, err := StringMapParam(map[string]any{"labels": map[string]any{"app": "web"}}, "labels")
	if err != nil || got["app"] != "web" {
		t.Errorf("labels = %v, %v", got, err)
	}
	if _, err := StringMapParam(map[string]any{"labels": map[string]any{"n": 1}}, "labels"); err == nil {
		t.Error("a number value was accepted")
	}
	if got, err := StringMapParam(map[string]any{}, "labels"); err != nil || got != nil {
		t.Errorf("absent = %v, %v", got, err)
	}
}
