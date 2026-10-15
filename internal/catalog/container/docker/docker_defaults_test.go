// Tests for container.docker.run's defaults: no network, read-only volumes,
// and a start from the image's ID.
package docker_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	dockermod "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container/docker"
)

// runWith runs container.docker.run for "web" with extra parameters.
func runWith(t *testing.T, h *harness, extra map[string]any) error {
	t.Helper()
	params := map[string]any{"name": "web", "image": "nginx:1.27"}
	for k, v := range extra {
		params[k] = v
	}
	_, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(params))
	return err
}

// TestRun_SecureByDefault: a task that says nothing gets a container on no
// network, with every volume read-only unless its options say rw, started
// from the image's ID and labeled with the reference it asked for.
func TestRun_SecureByDefault(t *testing.T) {
	h := newHarness(t, absent)
	if err := runWith(t, h, map[string]any{"volumes": []any{"/data:/data", "/cfg:/cfg:rw", "/x:/y:z", "/a:/b:ro,z"}}); err != nil {
		t.Fatal(err)
	}
	calls := h.invocations(t)
	if len(calls) != 1 {
		t.Fatalf("invocations = %v", calls)
	}
	for _, want := range []string{"--network=none", "--volume=/data:/data:ro", "--volume=/cfg:/cfg:rw", "--volume=/x:/y:z,ro",
		"--volume=/a:/b:ro,z", "--label=org.pleiades.image=nginx:1.27", " -- " + fakeImageID} {
		if !strings.Contains(calls[0], want) {
			t.Errorf("run = %q, want %q", calls[0], want)
		}
	}
	if got := h.rc.stats["image_id"]; got != fakeImageID {
		t.Errorf("image_id = %v, want %s", got, fakeImageID)
	}
}

// TestRun_PortsNeedANetwork: a container joins no network unless its task
// says so, so ports with no network are refused before anything is sent,
// naming the parameter; with one they are published.
func TestRun_PortsNeedANetwork(t *testing.T) {
	h := newHarness(t, absent)
	if err := runWith(t, h, map[string]any{"ports": []any{"8080:80"}}); err == nil || !strings.Contains(err.Error(), "set network") {
		t.Fatalf("err = %v, want ports without a network refused", err)
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("a refused run reached docker: %v", calls)
	}
	if err := runWith(t, h, map[string]any{"ports": []any{"8080:80"}, "network": "frontend"}); err != nil {
		t.Fatal(err)
	}
}

// TestRun_RefusesHostileNetworksAndVolumeOptions holds network and volume
// options to what docker accepts as those, so nothing else rides in.
func TestRun_RefusesHostileNetworksAndVolumeOptions(t *testing.T) {
	cases := []map[string]any{
		{"network": "--privileged"}, {"network": "container:db"}, {"network": "a b"}, {"network": "$(id)"},
		{"volumes": []any{"/a"}}, {"volumes": []any{"/a:/b:rw,--privileged"}}, {"volumes": []any{"/a:/b:exec"}}, {"volumes": []any{"/a:/b:c:d"}}, {"volumes": []any{":/b"}},
	}
	for _, extra := range cases {
		t.Run(fmt.Sprint(extra), func(t *testing.T) {
			h := newHarness(t, absent)
			if err := runWith(t, h, extra); err == nil {
				t.Fatalf("%v was accepted", extra)
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Fatalf("a refused value reached docker: %v", calls)
			}
		})
	}
}

// TestRun_PullsAnAbsentImageThenRunsItsID: an image the engine does not hold
// is pulled, by the reference asked for, and the container starts from the
// ID the engine then reports.
func TestRun_PullsAnAbsentImageThenRunsItsID(t *testing.T) {
	h := newHarness(t, containerFixture{imageAbsent: true})
	if err := runWith(t, h, nil); err != nil {
		t.Fatal(err)
	}
	calls := h.invocations(t)
	if len(calls) != 2 || calls[0] != "docker pull --quiet -- nginx:1.27" || !strings.Contains(calls[1], " -- "+fakeImageID) {
		t.Fatalf("invocations = %v, want a pull then a run by ID", calls)
	}
}

// TestRun_AFailedPullCreatesNothing: a pull the registry refuses fails the
// task, and no container is created.
func TestRun_AFailedPullCreatesNothing(t *testing.T) {
	h := newHarness(t, containerFixture{imageAbsent: true, pullExit: 1})
	if err := runWith(t, h, nil); err == nil || !strings.Contains(err.Error(), "pulling nginx:1.27") {
		t.Fatalf("err = %v, want the failed pull named", err)
	}
	if calls := h.invocations(t); len(calls) != 1 {
		t.Fatalf("invocations = %v, want the pull alone", calls)
	}
}

// TestRun_TheImageIDIsCheckedAndAFailedLookIsNotAnAbsence: an image inspect
// that could not reach the daemon is an error, never "absent" (which would
// pull), and an answer that is not an image ID is refused, since it becomes
// docker run's operand.
func TestRun_TheImageIDIsCheckedAndAFailedLookIsNotAnAbsence(t *testing.T) {
	for _, tc := range []struct {
		fixture containerFixture
		want    string
	}{
		{containerFixture{imageFails: true}, "could not read image nginx:1.27"},
		{containerFixture{imageID: "$(id)"}, "gave no image ID"},
		{containerFixture{imageID: "sha256:" + strings.Repeat("A", 64)}, "gave no image ID"},
	} {
		h := newHarness(t, tc.fixture)
		if err := runWith(t, h, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("err = %v, want %q", err, tc.want)
		}
		if calls := h.invocations(t); len(calls) != 0 {
			t.Fatalf("invocations = %v, want none", calls)
		}
	}
}

// TestRun_APullThatBringsNothingIsAFailure: a pull that exits 0 while the
// engine still holds no such image fails the task rather than running
// something else.
func TestRun_APullThatBringsNothingIsAFailure(t *testing.T) {
	h := newHarness(t, containerFixture{imageAbsent: true, pullNoop: true})
	if err := runWith(t, h, nil); err == nil || !strings.Contains(err.Error(), "holds no such image") {
		t.Fatalf("err = %v, want the missing image named", err)
	}
}

// TestRun_AnImageIDThatCannotBeRecordedCreatesNothing: the image ID is
// recorded before the container is created, so a run that cannot record it
// fails having created nothing.
func TestRun_AnImageIDThatCannotBeRecordedCreatesNothing(t *testing.T) {
	h := newHarness(t, absent)
	h.rc.failOnKey = "image_id"
	if err := runWith(t, h, nil); err == nil || !strings.Contains(err.Error(), "image_id") {
		t.Fatalf("err = %v, want the failed record surfaced", err)
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("invocations = %v, want none", calls)
	}
}
