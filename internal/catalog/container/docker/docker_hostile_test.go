// Tests that a hostile name or value is refused before anything reaches the
// device, and that a docker inspect which could not look is never read as an
// absent container.
package docker_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"

	dockermod "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container/docker"
)

// methodFunc is the shape of a Collection method's Invoke and Check.
type methodFunc func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error)

// TestRun_RefusesHostileValuesBeforeContact is LESSONS_LEARNED 137 held for
// docker run: quoting stops a shell, not option parsing, so every value that
// reaches docker's command line is checked first. Each case here would have
// been read by docker as an option (an image of --privileged runs a
// privileged container) or carried a metacharacter docker never allows.
func TestRun_RefusesHostileValuesBeforeContact(t *testing.T) {
	cases := []struct {
		field string
		value any
	}{
		{"name", "-f"}, {"name", "--help"}, {"name", "--privileged"}, {"name", "$(id)"}, {"name", "a;b"}, {"name", "web app"},
		{"image", "--privileged"}, {"image", "-v=/:/host"}, {"image", "--network=host"}, {"image", "$(id)"}, {"image", "nginx;id"}, {"image", "nginx id"},
		{"ports", []any{"--privileged"}}, {"ports", []any{"-v=/:/host"}}, {"ports", []any{""}}, {"ports", []any{"80\n81"}},
		{"volumes", []any{"--privileged"}}, {"volumes", []any{"-v=/:/host"}}, {"volumes", []any{"/a:/b\x00"}},
		{"restart_policy", "--privileged"}, {"restart_policy", "sometimes"}, {"restart_policy", "on-failure:x"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s=%q", tc.field, tc.value), func(t *testing.T) {
			h := newHarness(t, absent)
			params := map[string]any{"name": "web", "image": "nginx:1.27"}
			params[tc.field] = tc.value
			for _, m := range []methodFunc{dockermod.CheckRun, dockermod.Run} {
				if _, err := m(context.Background(), h.rc, h.device, h.params(params)); err == nil {
					t.Fatalf("%s %q was accepted", tc.field, tc.value)
				}
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Fatalf("a refused value reached docker: %v", calls)
			}
		})
	}
}

// TestStopAndRemove_RefuseHostileNamesBeforeContact holds the same rule for
// the name stop and remove pass to docker.
func TestStopAndRemove_RefuseHostileNamesBeforeContact(t *testing.T) {
	methods := map[string]methodFunc{
		"stop": dockermod.Stop, "check stop": dockermod.CheckStop,
		"remove": dockermod.Remove, "check remove": dockermod.CheckRemove,
	}
	for _, name := range []string{"-f", "--help", "--all", "$(id)", "a;b", "web app"} {
		for label, m := range methods {
			t.Run(label+" "+name, func(t *testing.T) {
				h := newHarness(t, running)
				if _, err := m(context.Background(), h.rc, h.device, h.params(map[string]any{"name": name})); err == nil {
					t.Fatalf("name %q was accepted", name)
				}
				if calls := h.invocations(t); len(calls) != 0 {
					t.Fatalf("a refused name reached docker: %v", calls)
				}
			})
		}
	}
}

// TestQueries_AnUnreachableDaemonIsNotAnAbsence is LESSONS_LEARNED 138's
// third answer: docker inspect exits 1 both for a missing container and for
// a daemon it cannot reach. Read as absent, the second had run create a
// container on an engine it never reached and stop or remove report a
// container they could not see as already gone; each must fail instead.
func TestQueries_AnUnreachableDaemonIsNotAnAbsence(t *testing.T) {
	methods := map[string]methodFunc{
		"run": dockermod.Run, "check run": dockermod.CheckRun,
		"stop": dockermod.Stop, "check stop": dockermod.CheckStop,
		"remove": dockermod.Remove, "check remove": dockermod.CheckRemove,
	}
	for label, m := range methods {
		t.Run(label, func(t *testing.T) {
			h := newHarness(t, containerFixture{inspectUnreachable: true})
			result, err := m(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27"}))
			if err == nil || !strings.Contains(err.Error(), "could not read container web") {
				t.Fatalf("result = %+v, err = %v; want the failed inspect reported", result, err)
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Fatalf("a method acted on a container it could not see: %v", calls)
			}
		})
	}
}
