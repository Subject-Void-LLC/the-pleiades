// Package firstparty reads the routing table the core ships
// (internal/api/wellknown's first-party.json, Phase 136a): which first-party
// Collection each first-party method belongs to, so a runbook naming one this
// build does not include is told which Collection holds it rather than only
// that the name is unknown.
package firstparty

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api/wellknown"
)

// document is the part of first-party.json this package reads.
type document struct {
	Groups []struct {
		Name string `json:"name"`
		Core bool   `json:"core"`
	} `json:"groups"`
	Methods map[string]string `json:"methods"`
}

var (
	once   sync.Once
	routes map[string]string
	core   map[string]bool
)

// load parses the embedded document once. A document that does not parse
// routes nothing: the hint is help, never a gate.
func load() {
	once.Do(func() {
		var d document
		if err := json.Unmarshal(wellknown.FirstParty, &d); err != nil {
			return
		}
		routes, core = d.Methods, map[string]bool{}
		for _, g := range d.Groups {
			core[g.Name] = g.Core
		}
	})
}

// Group returns the first-party group fqcn belongs to, when it is a method a
// first-party Collection provides.
func Group(fqcn string) (string, bool) {
	load()
	g, ok := routes[fqcn]
	return g, ok
}

// Hint is what to add to "not registered" for fqcn: the Collection that
// provides it and that this build does not include, or "" when fqcn is no
// first-party method (a typo, or a third party's).
func Hint(fqcn string) string {
	g, ok := Group(fqcn)
	if !ok {
		return ""
	}
	if core[g] {
		return fmt.Sprintf("; it is a core method (%s), so this build of The Pleiades is incomplete", g)
	}
	return fmt.Sprintf("; it belongs to the first-party %s Collection, which this build does not include", g)
}
