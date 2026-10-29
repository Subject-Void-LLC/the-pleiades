// A live test of Phase 131's R4 on Podman: every container.* lookup asked
// about something the engine does not have answers exists false, in a run
// and in a check, through a real session to a real Podman behind a real
// sshd, on a device onboarding built. The Docker side of the same rule is
// tested in each internal/catalog/container package against this machine's
// Docker.
package onboard

import (
	"context"
	"fmt"
	"testing"
	"time"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container/image"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container/network"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container/volume"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/container"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// absentRC is a RunbookContext holding a device's credential and keeping
// what a method records.
type absentRC struct {
	liveRC
	stats map[string]any
}

func (r *absentRC) SetStat(k string, v any) error  { r.stats[k] = v; return nil }
func (r *absentRC) EmitFact(k string, v any) error { return r.SetStat(k, v) }

// TestPodman_AbsentIsDataInBothModes: a container, a file in a container,
// a network, a volume and a registry tag that are not there are each exists
// false through rootless Podman, in a run and in a check.
func TestPodman_AbsentIsDataInBothModes(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Podman behind a real sshd")
	}
	d := testsupport.StartPodmanHost(t, testsupport.EngineHostOptions{})
	trustFixture(t, d)
	running := d.StartContainer(t, liveGateImage, testsupport.PodmanHostRootless)
	secrets := passwordFor(testsupport.PodmanHostRootless)
	device, _ := onboarded(t, container.TypeHost, map[string]inventory.PropertyValue{"host": d.Host, "port": d.Port}, secrets)

	absent := fmt.Sprintf("pleiades-absent-%d", time.Now().UnixNano())
	for _, tc := range []struct {
		fqcn   string
		params map[string]any
	}{
		{"container.info", map[string]any{"name": absent}},
		{"container.logs", map[string]any{"name": absent}},
		{"container.copy_out", map[string]any{"name": absent, "src": "/etc/hostname"}},
		{"container.copy_out", map[string]any{"name": running, "src": "/etc/pleiades-absent"}},
		{"container.network.info", map[string]any{"name": absent}},
		{"container.volume.info", map[string]any{"name": absent}},
		{"container.image.resolve", map[string]any{"image": "docker.io/library/alpine:pleiades-absent-tag-0"}},
	} {
		desc, ok := collection.Lookup(tc.fqcn)
		if !ok {
			t.Fatalf("%s is not registered", tc.fqcn)
		}
		for _, check := range []bool{false, true} {
			method := desc.Invoke
			if check {
				method = desc.Check
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			rc := &absentRC{liveRC: liveRC{secrets: secrets}, stats: map[string]any{}}
			res, err := method(ctx, rc, device, tc.params)
			cancel()
			if err != nil || res.Changed {
				t.Errorf("%s %v, check %v: %+v, %v", tc.fqcn, tc.params, check, res, err)
				continue
			}
			if rc.stats["exists"] != false {
				t.Errorf("%s %v, check %v: stats = %v, want exists false", tc.fqcn, tc.params, check, rc.stats)
			}
		}
	}
}
