// Which container each of a cluster's nodes is, on the container host the
// cluster's record names: proved by the node's name and its machine ID
// read from inside the container, never by name alone.
package onboard

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/kubeapi"
)

// FactNodeContainers is the fact naming each node's container.
const FactNodeContainers = "node_containers"

// Bounds on what the search reads from the engine host.
const (
	maxSearchContainers = 500
	maxMachineIDBytes   = 256
)

// peerRC is the RunbookContext a peer device's engine session runs with:
// the peer's own credential and nothing else.
type peerRC struct{ secrets map[string]string }

func (r peerRC) InjectSecrets() map[string]string { return r.secrets }
func (peerRC) SetStat(string, any) error          { return nil }
func (peerRC) EmitFact(string, any) error         { return nil }

// mapNodes finds, on the container host named hostName, the container each
// node runs in, and keeps only those whose /etc/machine-id equals the
// node's own machine ID. A node that reports none, or whose container is
// not found or does not match, is left out: a name alone proves nothing,
// since any container can be given any name. The session may only inspect,
// list and read files, so finding a node changes nothing on the host.
func mapNodes(ctx context.Context, nodes []kubeapi.Node, hostName string, peers Peers) ([]any, error) {
	host, secrets, err := peers.Device(ctx, hostName)
	if err != nil {
		return nil, fmt.Errorf("the node engine host %q: %w", hostName, err)
	}
	s, err := containerengine.Open(ctx, peerRC{secrets: secrets}, host, nil, "onboard.nodes",
		containerengine.OpContainerInspect, containerengine.OpContainerList, containerengine.OpContainerArchiveRead)
	if err != nil {
		return nil, fmt.Errorf("the node engine host %q: %w", hostName, err)
	}
	defer s.Close()
	engineID := host.(capability.EngineReach).EngineID(s.Kind)

	var out []any
	for _, n := range nodes {
		if n.MachineID == "" {
			continue
		}
		id, name, err := findNodeContainer(ctx, s, n.Name)
		if err != nil {
			return nil, err
		}
		if id == "" {
			continue
		}
		machine, err := readMachineID(ctx, s, id)
		if err != nil || machine != n.MachineID {
			continue
		}
		out = append(out, map[string]any{
			"node":           factText(n.Name),
			"engine":         string(s.Kind),
			"engine_id":      factText(string(engineID)),
			"container":      factText(id),
			"container_name": factText(name),
		})
	}
	return out, nil
}

// findNodeContainer returns the container a node of this name runs in: the
// container named after it (kind's convention, Docker Desktop's
// "desktop-control-plane"), or the one whose hostname it is (a node takes
// its container's hostname as its name, as k3s in a container does). Empty
// when neither exists.
func findNodeContainer(ctx context.Context, s *containerengine.Session, node string) (id, name string, err error) {
	if containerengine.ValidName("container", node) == nil {
		got, err := s.API.ContainerInspect(ctx, node, client.ContainerInspectOptions{})
		switch {
		case err == nil:
			return got.Container.ID, strings.TrimPrefix(got.Container.Name, "/"), nil
		case !cerrdefs.IsNotFound(err):
			return "", "", fmt.Errorf("inspecting container %q: %w", node, err)
		}
	}
	list, err := s.API.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return "", "", fmt.Errorf("listing containers: %w", err)
	}
	for i, c := range list.Items {
		if i == maxSearchContainers {
			break
		}
		got, err := s.API.ContainerInspect(ctx, c.ID, client.ContainerInspectOptions{})
		if err != nil || got.Container.Config == nil {
			continue
		}
		if got.Container.Config.Hostname == node {
			return got.Container.ID, strings.TrimPrefix(got.Container.Name, "/"), nil
		}
	}
	return "", "", nil
}

// readMachineID reads /etc/machine-id from inside a container through the
// archive API, which copies a file out without running anything in it.
func readMachineID(ctx context.Context, s *containerengine.Session, id string) (string, error) {
	res, err := s.API.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{SourcePath: "/etc/machine-id"})
	if err != nil {
		return "", err
	}
	defer res.Content.Close()
	tr := tar.NewReader(res.Content)
	if _, err := tr.Next(); err != nil {
		return "", fmt.Errorf("reading the machine ID archive: %w", err)
	}
	b, err := io.ReadAll(io.LimitReader(tr, maxMachineIDBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxMachineIDBytes {
		return "", errors.New("the machine ID is longer than any machine ID")
	}
	return strings.TrimSpace(string(b)), nil
}
