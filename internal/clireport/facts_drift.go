// Package clireport: the document `pleiades facts drift --json` prints
// (Phase 41d).
package clireport

import (
	"reflect"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// FactsDriftVersion is FactsDriftReport's schema_version.
const FactsDriftVersion = 1

// FactsDriftReport is each device's stable facts against its baseline, as
// the project's store holds them: which keys were added, removed or changed,
// with each value's times and keyed hashes, and a value shown only where it
// is not a digest.
type FactsDriftReport struct {
	// SchemaVersion is this document's major version.
	SchemaVersion int `json:"schema_version"`
	// At is when the drift was read, which every age is measured from.
	At time.Time `json:"at"`
	// Baseline is the baseline name asked for, absent when each device's
	// newest, its active baseline, was compared.
	Baseline string `json:"baseline,omitempty"`
	// Devices is each device's drift, in the order asked.
	Devices []facts.DriftReport `json:"devices"`
}

// FactsDriftSchema is FactsDriftReport's.
var FactsDriftSchema = Schema{
	File: "facts-drift.json", Command: "pleiades facts drift --json", Title: "Pleiades facts drift",
	Model: reflect.TypeFor[FactsDriftReport](),
	Known: map[string][]string{
		"DriftReport.State": {string(facts.DriftInSync), string(facts.DriftDrifted), string(facts.DriftUnknown), string(facts.DriftNoBaseline)},
		"KeyDrift.Change":   {string(facts.ChangeAdded), string(facts.ChangeRemoved), string(facts.ChangeChanged)},
	},
	ModelFiles: []string{"internal/clireport/facts_drift.go", "internal/facts/drift.go"},
}
