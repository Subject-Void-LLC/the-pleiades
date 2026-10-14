// EngineRoutes: the plan line naming how each container task will reach its
// device's engine, decided the way the run decides it.
package validate

import (
	"fmt"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
)

// EngineRoutes names, for every task of a container method, the engine and
// carrier its session will use on each device, through
// containerengine.Route: the function a session itself chooses with, so
// the plan and the run cannot disagree. A task whose device has no usable
// way to its engine is left to TransportRule and the run, which refuse it.
func EngineRoutes(world WorldView) []Route {
	var routes []Route
	for id, task := range world.DAG.Nodes {
		desc, ok := collection.Lookup(task.FQCN)
		if !ok || !reachesAnEngine(desc.Manifest) {
			continue
		}
		kind := engineOf(desc.Manifest)
		_, devices := world.taskDevices(task)
		for _, dev := range devices {
			engine, carrier, err := containerengine.Route(dev, task.Params, kind)
			if err != nil {
				continue
			}
			routes = append(routes, Route{
				Node:      id,
				Device:    dev.ID(),
				Transport: carrier.Transport(),
				Detail:    fmt.Sprintf("the %s engine", engine),
			})
		}
	}
	return routes
}

// reachesAnEngine reports whether a method reaches its device only through
// a container engine's API.
func reachesAnEngine(m collection.Manifest) bool {
	if len(m.SupportedTransports) == 0 {
		return false
	}
	for _, t := range m.SupportedTransports {
		if !slices.Contains(capability.EngineTransports(), t) {
			return false
		}
	}
	return true
}

// engineOf is the engine an engine-only method requires (container.docker.*
// requires Docker), or empty for one that reaches whichever the device runs.
func engineOf(m collection.Manifest) capability.EngineKind {
	switch {
	case slices.Contains(m.RequiredCapabilities, capability.NameDocker):
		return capability.EngineDocker
	case slices.Contains(m.RequiredCapabilities, capability.NamePodman):
		return capability.EnginePodman
	}
	return ""
}

func init() {
	RegisterPlanner(EngineRoutes)
}
