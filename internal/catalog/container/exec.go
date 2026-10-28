// container.exec and container.recreate: a command inside a running
// container, and a container replaced by its declaration.
package container

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const (
	paramCmd     = "cmd"
	statExitCode = "exit_code"

	fqcnRecreate = "container.recreate"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "container.exec",
		Manifest: func() collection.Manifest {
			m := containerengine.MethodManifest(collection.Reversibility{
				Reversible: false,
				Notes:      "A command's effect inside a container is unknown to this platform, as exec.command's is on a host.",
			}, execDoc())
			m.SupportsCheck = false
			m.NoCheckReason = "a command run inside a container can change anything the container can reach, and what it changes is known only once it has run"
			return m
		}(),
		Invoke: Exec,
	})
}

// Exec implements container.exec: one command through the running
// container's own /bin/sh -c, on whichever engine the device runs. A
// non-zero exit is an error, after its output is recorded.
func Exec(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "container.exec"
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
	cmd, err := sdk.RequiredStringParam(params, paramCmd)
	if err != nil {
		return fail(err)
	}
	s, err := open(ctx, rc, device, params, fqcn, containerengine.ExecOps...)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	res, err := s.Exec(ctx, name, cmd, containerengine.ExecOptions{})
	if err != nil {
		return fail(err)
	}
	if err := recordStats(rc, map[string]any{statName: name, statExitCode: res.ExitCode, statStdout: res.Stdout, statStderr: res.Stderr}); err != nil {
		return collection.Result{}, err
	}
	if res.ExitCode != 0 {
		return fail(fmt.Errorf("the command exited %d: %s", res.ExitCode, farside.Output(firstNonEmpty(res.Stderr, res.Stdout))))
	}
	return collection.Result{Changed: true}, nil
}

// firstNonEmpty is the first of values that is not only white space, or
// the empty string. It picks which stream speaks for a failed command; the
// caller bounds and quotes it with farside.Output before an error holds it.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
