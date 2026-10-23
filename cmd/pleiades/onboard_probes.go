// Command pleiades's `onboard`: running each loaded external program's
// probe for the capability it defines, and recording what it grants.
package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/onboard"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The stats a probe method sets: whether the device answered, and the
// grant's properties when it did (capability.DataDefinition.Probe).
const (
	probeStatGranted    = "granted"
	probeStatProperties = "properties"
)

// runProgramProbes runs, against the device name, the probe of every
// capability a loaded external program defines with one, through the same
// pipeline `pleiades adhoc` runs a method through (its credential, its
// route, its journal, its confinement), and records what each decides: a
// probed grant, bound to the properties the device's type binds, when the
// device answered and holds every compiled capability the definition
// requires; the withdrawal of an earlier probed grant of the same program
// when it did not. An operator's assertion is never withdrawn by a probe.
// It returns nothing when no program is loaded.
func runProgramProbes(dir, name string) ([]onboard.ProbedGrant, error) {
	set, err := loadExternalCollections(context.Background(), dir)
	if err != nil || set == nil {
		return nil, err
	}
	var defs []capability.DataDefinition
	for _, d := range capability.All() {
		if d.Data != nil && d.Data.Probe != "" {
			defs = append(defs, *d.Data)
		}
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })

	var results []onboard.ProbedGrant
	for _, def := range defs {
		result := onboard.ProbedGrant{Capability: string(def.Name), Program: def.Provider, Probe: def.Probe}
		granted, props, err := runProbe(dir, name, def)
		if err == nil {
			result.Granted, result.Withdrawn, err = recordProbe(dir, name, def, granted, props)
		}
		if err != nil {
			result.Granted, result.Error = false, err.Error()
		}
		results = append(results, result)
	}
	return results, nil
}

// runProbe runs def's probe on the device name and returns what it found.
func runProbe(dir, name string, def capability.DataDefinition) (bool, map[string]any, error) {
	payload, err := adhocRunbook(name, def.Probe, nil, 0)
	if err != nil {
		return false, nil, err
	}
	opts := runOptions{dir: dir, mode: collection.ModeExecute, forks: 1, persist: true, asJSON: true}
	rep, err := carryOut(opts, runSource{label: fmt.Sprintf("probe %s on %s", def.Probe, name), yaml: payload})
	if err != nil {
		return false, nil, fmt.Errorf("the probe %s failed: %w", def.Probe, err)
	}
	if len(rep.Tasks) != 1 || rep.Tasks[0].Error != "" {
		reason := "it reported nothing"
		if len(rep.Tasks) == 1 {
			reason = rep.Tasks[0].Error
		}
		return false, nil, fmt.Errorf("the probe %s failed: %s", def.Probe, reason)
	}
	stats := rep.Tasks[0].Stats
	granted, _ := stats[probeStatGranted].(bool)
	props, _ := stats[probeStatProperties].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	return granted, props, nil
}

// recordProbe writes what a probe decided to the device name: a probed
// grant when it granted, the withdrawal of this program's earlier probed
// grant when it did not. It reports granted and withdrawn.
func recordProbe(dir, name string, def capability.DataDefinition, granted bool, props map[string]any) (bool, bool, error) {
	ctx := context.Background()
	factory := inv.NewItemFactory()
	repo := inv.NewFileRepository(filepath.Join(dir, inv.DefaultInventoryFilename), factory)
	item, err := repo.GetByName(ctx, name)
	if err != nil {
		return false, false, err
	}
	g, ok := item.(granting)
	if !ok {
		return false, false, fmt.Errorf("host %q cannot hold a granted capability", name)
	}
	if !granted {
		existing, _, err := inventory.GrantsFrom(item.Properties())
		if err != nil {
			return false, false, err
		}
		for _, e := range existing {
			if e.Capability == def.Name && e.Source == inventory.GrantProbed && e.Program == def.Provider {
				if _, err := g.RevokeGrant(def.Name); err != nil {
					return false, false, err
				}
				return false, true, repo.Save(ctx, item)
			}
		}
		return false, false, nil
	}
	if err := capability.ValidateGrant(def, props); err != nil {
		return false, false, fmt.Errorf("the probe %s granted properties its own definition refuses: %w", def.Probe, err)
	}
	deviceType := ""
	if typed, ok := item.(interface{ DeviceType() string }); ok {
		deviceType = typed.DeviceType()
	}
	if err := g.RecordGrant(inventory.Grant{
		Capability: def.Name,
		Source:     inventory.GrantProbed,
		Program:    def.Provider,
		Properties: props,
		At:         time.Now().UTC(),
		Binding:    record.DiscoveryBinding(deviceType, item.Properties()),
	}); err != nil {
		return false, false, err
	}
	rebuilt, err := factory.Rebuilt(item)
	if err != nil {
		return false, false, err
	}
	if !rebuilt.HasCapability(def.Name) {
		return false, false, fmt.Errorf("the device answered, but it cannot hold %s, which requires %s beside it", def.Name, requirementsText(def))
	}
	return true, false, repo.Save(ctx, item)
}
