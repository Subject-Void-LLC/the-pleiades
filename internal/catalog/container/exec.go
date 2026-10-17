// container.exec and container.recreate: a command inside a running
// container, and a container replaced by its declaration.
package container

import (
	"context"
	"fmt"
	"strings"

	moby "github.com/moby/moby/api/types/container"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
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
			m := manifest(collection.Reversibility{
				Reversible: false,
				Notes:      "A command's effect inside a container is unknown to this platform, as exec.command's is on a host.",
			}, execDoc())
			m.SupportsCheck = false
			m.NoCheckReason = "a command run inside a container can change anything the container can reach, and what it changes is known only once it has run"
			return m
		}(),
		Invoke: Exec,
	})
	collection.MustRegister(collection.Descriptor{
		Name: fqcnRecreate,
		Manifest: manifest(collection.Reversibility{
			Reversible: false,
			Notes:      "Recreating a container removes the old one, whose writable layer and anonymous volumes are gone; its declaration can make a new one, never the old.",
		}, recreateDoc()),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return recreate(ctx, rc, d, p, collection.ModeExecute)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return recreate(ctx, rc, d, p, collection.ModeCheck)
		},
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
		return fail(fmt.Errorf("the command exited %d: %s", res.ExitCode, strings.TrimSpace(firstNonEmpty(res.Stderr, res.Stdout))))
	}
	return collection.Result{Changed: true}, nil
}

// firstNonEmpty is the first of values that is not empty, cut to a size
// an error can carry.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			const limit = 2048
			if len(v) > limit {
				return v[len(v)-limit:]
			}
			return v
		}
	}
	return ""
}

// recreate is container.recreate's body: an absent container is created
// and started; one that matches the declaration is left alone (started if
// stopped) unless force is set; one that differs, or any with force, is
// removed and made again from the declaration.
func recreate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRecreate, err)
	}
	force, err := sdk.BoolParamOr(params, paramForce, false)
	if err != nil {
		return fail(err)
	}
	spec, err := containerengine.ParseSpec(params)
	if err != nil {
		return fail(err)
	}
	if spec.Image == "" {
		return fail(fmt.Errorf("image is required"))
	}
	auth, err := containerengine.RegistryAuth(rc, spec.Image)
	if err != nil {
		return fail(err)
	}
	s, err := open(ctx, rc, device, params, fqcnRecreate, convergeOps...)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	live, exists, err := inspect(ctx, s, spec.Name)
	if err != nil {
		return fail(err)
	}
	if err := guardIDIf(params, live, exists); err != nil {
		return fail(err)
	}
	img, err := presentImage(ctx, s, spec, auth, mode)
	if err != nil {
		return fail(err)
	}
	spec.ImageID = img.ID
	if err := recordStats(rc, map[string]any{
		statName: spec.Name, statImage: img.Pinned, statDigest: img.Digest, statImageID: img.ID,
		statEnv: spec.EnvNames(), statPulled: img.Pulled,
	}); err != nil {
		return collection.Result{}, err
	}
	var diffs []string
	if exists {
		if img.ID == "" {
			diffs = []string{"image: the declared image is not on the engine yet"}
		} else {
			diffs = spec.Differences(live)
		}
		if err := recordStats(rc, map[string]any{statID: live.ID, "differences": diffs}); err != nil {
			return collection.Result{}, err
		}
	}
	if exists && len(diffs) == 0 && !force {
		return existing(ctx, rc, s, spec, live, img, mode, true, false)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, rc.SetStat(statState, "running")
	}
	if exists {
		removeQuietly(s, live.ID)
		if _, still, err := inspect(ctx, s, spec.Name); err != nil || still {
			return fail(fmt.Errorf("container %s could not be removed to recreate it", spec.Name))
		}
	}
	// A recreate records no undo: the old container cannot come back.
	res, err := createNew(ctx, rc, s, spec, true, false)
	if err != nil {
		return fail(err)
	}
	return res, nil
}

// guardIDIf applies guardID to a container that exists.
func guardIDIf(params map[string]any, live moby.InspectResponse, exists bool) error {
	if !exists {
		return nil
	}
	return guardID(params, live)
}
