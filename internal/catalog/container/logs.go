// container.logs: a container's output, bounded.
package container

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() { registerRead("container.logs", Logs) }

// Logs implements container.logs: the last lines a container wrote,
// bounded, or exists false for a container that is not there.
func Logs(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "container.logs"
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return fail(err)
	}
	if err := containerengine.ValidName("container", name); err != nil {
		return fail(err)
	}
	tail, set, err := sdk.IntParam(params, paramTail)
	if err != nil {
		return fail(err)
	}
	if !set {
		tail = defaultTail
	}
	if tail < 1 || tail > maxTail {
		return fail(fmt.Errorf("%s: %d is not a number of lines from 1 to %d", paramTail, tail, maxTail))
	}
	since, err := containerengine.TextParam(params, paramSince)
	if err != nil {
		return fail(err)
	}
	if since != "" && strings.ContainsAny(since, "&?#/ ") {
		return fail(fmt.Errorf("%s: %q is not a timestamp or a duration", paramSince, since))
	}
	s, err := open(ctx, rc, device, params, fqcn, append([]containerengine.Op{containerengine.OpContainerInspect}, containerengine.LogsOps...)...)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	c, exists, err := inspect(ctx, s, name)
	if err != nil {
		return fail(err)
	}
	// A container that is not there is an answer, exists false, as
	// container.info gives it, so a plan can branch on it in a run and in a
	// check alike.
	if !exists {
		return collection.Result{}, recordStats(rc, map[string]any{statName: name, statExists: false})
	}
	tty := c.Config != nil && c.Config.Tty
	stdout, stderr, err := s.Logs(ctx, c.ID, tail, since, tty, maxLogBytes)
	if err != nil {
		return fail(err)
	}
	return collection.Result{}, recordStats(rc, map[string]any{statName: name, statExists: true, statID: c.ID, statStdout: stdout, statStderr: stderr})
}
