// Command pleiades's `onboard`: running each loaded external program's
// probe for the capability it defines, and recording what it grants.
package main

import (
	"context"
	"fmt"
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
// route, its journal, its confinement), each bounded by timeout (onboard's
// --timeout), and records what each decides: a probed grant, bound to the
// properties the device's type binds and to the definition's digest, and
// naming the program build that probed, when the device answered and holds
// every compiled capability the definition requires; the withdrawal of an
// earlier probed grant of the same program when it did not. An operator's
// assertion is never withdrawn by a probe. It returns nothing when no
// program is loaded.
func runProgramProbes(dir, name string, timeout time.Duration) ([]onboard.ProbedGrant, error) {
	set, err := loadExternalCollections(context.Background(), dir)
	if err != nil || set == nil {
		return nil, err
	}
	// Which build defined each capability: the digest the loader checked
	// before it ran the program, recorded with a probed grant as provenance.
	builds := map[capability.Name]string{}
	for _, p := range set.Programs() {
		for _, c := range p.Capabilities {
			builds[capability.Name(c)] = p.Digest
		}
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
		granted, props, err := runProbe(dir, name, def, timeout)
		if err == nil {
			result.Granted, result.Withdrawn, err = recordProbe(dir, name, def, builds[def.Name], granted, props)
		}
		if err != nil {
			result.Granted, result.Error = false, err.Error()
		}
		results = append(results, result)
	}
	return results, nil
}

// runProbe runs def's probe on the device name and returns what it found.
// A probe still running after timeout is stopped (the loader ends the
// program's process when its context ends) and reported as failed.
func runProbe(dir, name string, def capability.DataDefinition, timeout time.Duration) (bool, map[string]any, error) {
	payload, err := adhocRunbook(name, def.Probe, nil, 0)
	if err != nil {
		return false, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	opts := runOptions{ctx: ctx, dir: dir, mode: collection.ModeExecute, forks: 1, persist: true, asJSON: true}
	rep, err := carryOut(opts, runSource{label: fmt.Sprintf("probe %s on %s", def.Probe, name), yaml: payload})
	if ctx.Err() != nil {
		return false, nil, fmt.Errorf("the probe %s did not finish within %s and was stopped", def.Probe, timeout)
	}
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
	granted, props := probeOutcome(rep.Tasks[0].Stats)
	return granted, props, nil
}

// probeOutcome reads what a probe decided from the stats its run reported
// (already masked by the run's known secrets): granted only when the stat
// is the boolean true, and the grant's properties only when they are a
// mapping. Everything else the probe set is dropped here, so nothing but
// declared properties can reach the grant, and recordProbe holds those to
// the definition.
func probeOutcome(stats map[string]any) (bool, map[string]any) {
	granted, _ := stats[probeStatGranted].(bool)
	props, _ := stats[probeStatProperties].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	return granted, props
}

// probedGrant is the grant a probe of def that answered with props makes:
// held to def (every required property, none undeclared, each of its
// type), bound to the device's bound properties (binding) and to def's
// digest, and naming build, the program build that probed. A probe whose
// properties its own definition refuses grants nothing.
func probedGrant(def capability.DataDefinition, build, binding string, props map[string]any) (inventory.Grant, error) {
	if err := capability.ValidateGrant(def, props); err != nil {
		return inventory.Grant{}, fmt.Errorf("the probe %s granted properties its own definition refuses: %w", def.Probe, err)
	}
	return inventory.Grant{
		Capability: def.Name,
		Source:     inventory.GrantProbed,
		Program:    def.Provider,
		Properties: props,
		At:         time.Now().UTC(),
		Binding:    binding,
		Definition: capability.DefinitionDigest(def),
		Build:      build,
	}, nil
}

// recordProbe writes what a probe decided to the device name: a probed
// grant when it granted, the withdrawal of this program's earlier probed
// grant when it did not. build is the probing program's digest. It reports
// granted and withdrawn.
func recordProbe(dir, name string, def capability.DataDefinition, build string, granted bool, props map[string]any) (bool, bool, error) {
	ctx := context.Background()
	factory := inv.NewItemFactory()
	repo := inv.NewFileRepository(inventoryFile(dir), factory)
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
	deviceType := ""
	if typed, ok := item.(interface{ DeviceType() string }); ok {
		deviceType = typed.DeviceType()
	}
	grant, err := probedGrant(def, build, record.DiscoveryBinding(deviceType, item.Properties()), props)
	if err != nil {
		return false, false, err
	}
	if err := g.RecordGrant(grant); err != nil {
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
