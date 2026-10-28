// container.prune of volumes, networks and images, and container.copy_in
// from a file in the run's project, against the real engine.
package container_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestPrune_VolumesNetworksAndImages: an anonymous volume and an unused
// network carrying the test's label are listed by a check and removed by
// the run; a named volume with the same label is left, since someone named
// it; images narrowed to a label nothing carries list nothing.
func TestPrune_VolumesNetworksAndImages(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	tag := h.name(t)
	label := map[string]string{"pleiades.test": tag}
	labels := []any{"pleiades.test=" + tag}

	anon, err := h.api.VolumeCreate(ctx, client.VolumeCreateOptions{Driver: "local", Labels: label})
	if err != nil {
		t.Fatal(err)
	}
	named := tag + "-named"
	if _, err := h.api.VolumeCreate(ctx, client.VolumeCreateOptions{Name: named, Driver: "local", Labels: label}); err != nil {
		t.Fatal(err)
	}
	netName := tag + "-net"
	if _, err := h.api.NetworkCreate(ctx, netName, client.NetworkCreateOptions{Driver: "bridge", Labels: label}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, v := range []string{anon.Volume.Name, named} {
			_, _ = h.api.VolumeRemove(ctx, v, client.VolumeRemoveOptions{Force: true})
		}
		_, _ = h.api.NetworkRemove(ctx, netName, client.NetworkRemoveOptions{})
	})
	volumeExists := func(name string) bool {
		_, err := h.api.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
		return err == nil
	}

	rc := fresh()
	vols := map[string]any{"what": "volumes", "labels": labels}
	if res, err := call(t, h, rc, "container.prune", true, vols); err != nil || !res.Changed || !volumeExists(anon.Volume.Name) {
		t.Fatalf("a check of a volume prune = %+v, %v", res, err)
	}
	if pruned, _ := rc.stats["pruned"].([]string); len(pruned) != 1 || pruned[0] != anon.Volume.Name {
		t.Errorf("the check listed %v, want only the anonymous volume", rc.stats["pruned"])
	}
	if res, err := call(t, h, fresh(), "container.prune", false, vols); err != nil || !res.Changed || volumeExists(anon.Volume.Name) || !volumeExists(named) {
		t.Errorf("a volume prune = %+v, %v; anonymous there %v, named there %v", res, err, volumeExists(anon.Volume.Name), volumeExists(named))
	}

	rc = fresh()
	nets := map[string]any{"what": "networks", "labels": labels}
	if res, err := call(t, h, rc, "container.prune", false, nets); err != nil || !res.Changed {
		t.Fatalf("a network prune = %+v, %v", res, err)
	}
	if pruned, _ := rc.stats["pruned"].([]string); len(pruned) != 1 || pruned[0] != netName {
		t.Errorf("the network prune listed %v", rc.stats["pruned"])
	}
	if _, err := h.api.NetworkInspect(ctx, netName, client.NetworkInspectOptions{}); err == nil {
		t.Error("the pruned network is still there")
	}

	if res, err := call(t, h, fresh(), "container.prune", false, map[string]any{"what": "images", "labels": labels}); err != nil || res.Changed {
		t.Errorf("an image prune narrowed to an unused label = %+v, %v; want no change", res, err)
	}
	if _, err := call(t, h, fresh(), "container.prune", false, map[string]any{"what": "volumes", "labels": []any{" "}}); err == nil {
		t.Error("a blank label was accepted")
	}
}

// projectRecorder is a recorder whose run holds a project's files.
type projectRecorder struct {
	*recorder
	files map[string][]byte
}

func (p *projectRecorder) ReadProjectFile(name string, limit int64) ([]byte, error) {
	data, ok := p.files[name]
	if !ok {
		return nil, fmt.Errorf("%s: no such file in the project", name)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is over %d bytes", name, limit)
	}
	return data, nil
}

// runnerRecorder is a recorder on a run with no project here, as a
// Runner's is.
type runnerRecorder struct{ *recorder }

func (runnerRecorder) ReadProjectFile(string, int64) ([]byte, error) {
	return nil, sdk.ErrNoProjectFiles
}

// TestCopyIn_FromTheProject: src reads the run's project; a run without
// one, as on a Runner, is refused naming content; content and src
// together, a missing source and a mode that is not octal are refused.
func TestCopyIn_FromTheProject(t *testing.T) {
	h := newHarness(t)
	name := h.name(t)
	if _, err := call(t, h, fresh(), "container.run", false, map[string]any{"name": name, "image": h.nginx}); err != nil {
		t.Fatal(err)
	}
	in, _ := collection.Lookup("container.copy_in")
	ctx := context.Background()
	put := map[string]any{"name": name, "dest": "/tmp/from-project.txt", "src": "files/app.conf"}

	project := &projectRecorder{recorder: fresh(), files: map[string][]byte{"files/app.conf": []byte("listen 8080\n")}}
	if res, err := in.Invoke(ctx, project, h.device, put); err != nil || !res.Changed {
		t.Fatalf("copy_in from the project = %+v, %v", res, err)
	}
	rc := fresh()
	if _, err := call(t, h, rc, "container.copy_out", false, map[string]any{"name": name, "src": "/tmp/from-project.txt"}); err != nil || rc.stats["content"] != "listen 8080\n" {
		t.Fatalf("copy_out = %v, %v", rc.stats["content"], err)
	}

	for label, rc := range map[string]sdk.RunbookContext{"a Runner's run": runnerRecorder{fresh()}, "a context with no project": fresh()} {
		if _, err := in.Invoke(ctx, rc, h.device, put); err == nil || !strings.Contains(err.Error(), "content") {
			t.Errorf("%s: err = %v, want a refusal naming content", label, err)
		}
	}
	for label, params := range map[string]map[string]any{
		"content and src":   {"name": name, "dest": "/tmp/x", "src": "files/app.conf", "content": "x"},
		"a missing source":  {"name": name, "dest": "/tmp/x", "src": "files/absent"},
		"neither":           {"name": name, "dest": "/tmp/x"},
		"a mode not octal":  {"name": name, "dest": "/tmp/x", "content": "x", "mode": "rw-r--r--"},
		"content not text":  {"name": name, "dest": "/tmp/x", "content": 7},
		"a climbing dest":   {"name": name, "dest": "/tmp/../etc/x", "content": "x"},
		"a bad output size": {"name": name, "src": "/tmp/from-project.txt", "max_bytes": 0},
	} {
		fqcn := "container.copy_in"
		if _, ok := params["max_bytes"]; ok {
			fqcn = "container.copy_out"
		}
		if _, err := call(t, h, &recorder{stats: map[string]any{}}, fqcn, false, params); err == nil {
			t.Errorf("%s was accepted", label)
		}
	}
	// An absent file is an answer, exists false, in a run and in a check
	// (Phase 131, R4).
	for _, check := range []bool{false, true} {
		rc := fresh()
		if _, err := call(t, h, rc, "container.copy_out", check, map[string]any{"name": name, "src": "/tmp/absent"}); err != nil || rc.stats["exists"] != false || rc.stats["content"] != nil {
			t.Errorf("check %v: copy_out of an absent file = %v, %v", check, rc.stats, err)
		}
	}
}
