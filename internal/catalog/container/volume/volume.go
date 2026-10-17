// Package volume implements the container.volume.* methods (Phase 119e): an
// engine's named volumes, Docker's or Podman's, created, removed and
// inspected through the engine's own API (pkg/containerengine), never its
// command line.
//
// Every method opens one guarded session declaring the operations it sends.
// create and remove read, compare and then write through one body their
// checks share (the Phase 46 pattern).
package volume

import (
	"context"
	"fmt"
	"slices"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	moby "github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameters shared by the namespace's methods.
const (
	paramName      = "name"
	paramCreatedAt = "created_at"
	paramDriver    = "driver"
	paramLabels    = "labels"
)

// Returns shared by the namespace's methods.
const (
	statName      = "name"
	statExists    = "exists"
	statDriver    = "driver"
	statCreatedAt = "created_at"
)

// driverLocal is the one driver a task may name: the engine's own, which
// both Docker and Podman have. A plugin's driver is not offered.
const driverLocal = "local"

// drivers is the allowlist of driver names.
var drivers = []string{driverLocal}

// inspectVolume reads the volume named name, reporting false when the
// engine has none by that name.
func inspectVolume(ctx context.Context, s *containerengine.Session, name string) (moby.Volume, bool, error) {
	res, err := s.API.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return moby.Volume{}, false, nil
	}
	if err != nil {
		return moby.Volume{}, false, fmt.Errorf("reading volume %s: %w", name, err)
	}
	if res.Volume.Name != name {
		return moby.Volume{}, false, fmt.Errorf("the engine answered volume %q when asked for %s", res.Volume.Name, name)
	}
	return res.Volume, true, nil
}

// validName refuses a volume name an engine would read as something else.
func validName(params map[string]any) (string, error) {
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return "", err
	}
	return name, containerengine.ValidName("volume", name)
}

// guardCreated refuses a volume that is not the one the task recorded: a
// volume has no ID beside its name, so the moment the engine made it
// stands in for one, and an undo recorded against one volume never removes
// another made under its name since.
func guardCreated(params map[string]any, v moby.Volume) error {
	want := sdk.StringParam(params, paramCreatedAt)
	if want == "" || want == v.CreatedAt {
		return nil
	}
	return fmt.Errorf("volume %s was made at %s, not %s, so this task, recorded against the other, does nothing to it", v.Name, v.CreatedAt, want)
}

// usersOf names the containers, running or stopped, that mount the
// volume.
func usersOf(ctx context.Context, s *containerengine.Session, name string) ([]string, error) {
	res, err := s.API.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: make(client.Filters).Add("volume", name)})
	if err != nil {
		return nil, fmt.Errorf("listing the containers using volume %s: %w", name, err)
	}
	var names []string
	for _, c := range res.Items {
		n := c.ID
		if len(c.Names) > 0 {
			n = strings.TrimPrefix(c.Names[0], "/")
		}
		names = append(names, n)
	}
	slices.Sort(names)
	return names, nil
}

// recordStats records each stat, stopping at the first failure.
func recordStats(rc sdk.RunbookContext, stats map[string]any) error {
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return err
		}
	}
	return nil
}
