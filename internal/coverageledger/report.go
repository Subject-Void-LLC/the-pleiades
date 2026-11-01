// Package coverageledger: what a ledger adds up to, and the floor each
// collection is held to.
package coverageledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// OpStatus is where one contract operation stands.
type OpStatus string

// The operation statuses.
const (
	// OpMapped is an operation some method implements.
	OpMapped OpStatus = "mapped"
	// OpExcluded is an operation out of the collection's scope, with a reason.
	OpExcluded OpStatus = "excluded"
	// OpUnmapped is a gap: no method implements it and nothing excludes it.
	OpUnmapped OpStatus = "unmapped"
)

// OpRow is one operation in a report.
type OpRow struct {
	Op
	// Status is where the operation stands.
	Status OpStatus `json:"status"`
	// Methods implement it, when it is mapped.
	Methods []string `json:"methods,omitempty"`
	// Reason is why it is excluded, when it is.
	Reason string `json:"reason,omitempty"`
	// Live is whether every method implementing it is proven live on some OS.
	Live bool `json:"live"`
}

// MethodRow is one method of the collection in a report.
type MethodRow struct {
	// FQCN names the method.
	FQCN string `json:"fqcn"`
	// Ops are the operations it implements.
	Ops []string `json:"ops,omitempty"`
	// WithoutOp is why it implements no single operation, when it does not.
	WithoutOp string `json:"withoutOp,omitempty"`
	// Proof is how it is proven, per OS.
	Proof map[OS]Proof `json:"proof,omitempty"`
}

// Report is what one collection's ledger adds up to.
type Report struct {
	// Collection is the FQCN prefix.
	Collection string `json:"collection"`
	// Title names the endpoint.
	Title string `json:"title"`
	// Source is where the contract came from.
	Source Source `json:"source"`
	// Total, Mapped, Excluded and Unmapped count operations.
	Total    int `json:"total"`
	Mapped   int `json:"mapped"`
	Excluded int `json:"excluded"`
	Unmapped int `json:"unmapped"`
	// Live counts mapped operations whose every method is proven live.
	Live int `json:"live"`
	// MappedPercent is Mapped over the operations in scope (Total less
	// Excluded), to one decimal place.
	MappedPercent float64 `json:"mappedPercent"`
	// LivePercent is Live over the operations in scope, to one decimal place.
	LivePercent float64 `json:"livePercent"`
	// Ops is every operation, in the contract's order.
	Ops []OpRow `json:"ops"`
	// Methods is every method the ledger names, ordered by FQCN.
	Methods []MethodRow `json:"methods"`
}

// Compute adds up l. methods is every registered FQCN in the collection, so
// a method the ledger never mentions still has a row.
func (l Ledger) Compute(methods []string) Report {
	r := Report{Collection: l.Contract.Collection, Title: l.Contract.Title, Source: l.Contract.Source}
	rows := map[string]*MethodRow{}
	row := func(fqcn string) *MethodRow {
		if rows[fqcn] == nil {
			rows[fqcn] = &MethodRow{FQCN: fqcn, Proof: l.Map.Proof[fqcn]}
		}
		return rows[fqcn]
	}
	for _, fqcn := range methods {
		row(fqcn)
	}
	for fqcn, why := range l.Map.MethodsWithoutOp {
		row(fqcn).WithoutOp = why
	}
	for _, op := range l.Contract.Ops {
		m, listed := l.Map.Ops[op.ID]
		opRow := OpRow{Op: op, Status: OpUnmapped}
		switch {
		case listed && len(m.Methods) > 0:
			opRow.Status, opRow.Methods, opRow.Live = OpMapped, m.Methods, true
			for _, fqcn := range m.Methods {
				row(fqcn).Ops = append(row(fqcn).Ops, op.ID)
				opRow.Live = opRow.Live && provenLive(l.Map.Proof[fqcn])
			}
			r.Mapped++
			if opRow.Live {
				r.Live++
			}
		case listed && m.Excluded != "":
			opRow.Status, opRow.Reason = OpExcluded, m.Excluded
			r.Excluded++
		default:
			r.Unmapped++
		}
		r.Ops = append(r.Ops, opRow)
	}
	r.Total = len(l.Contract.Ops)
	r.MappedPercent = percent(r.Mapped, r.Total-r.Excluded)
	r.LivePercent = percent(r.Live, r.Total-r.Excluded)
	for _, mr := range rows {
		r.Methods = append(r.Methods, *mr)
	}
	sort.Slice(r.Methods, func(i, j int) bool { return r.Methods[i].FQCN < r.Methods[j].FQCN })
	return r
}

// MethodsIn returns the FQCNs among names that belong to collection (start
// with its prefix and a dot), in order: the methods a ledger is held to.
func MethodsIn(collection string, names []string) []string {
	var out []string
	for _, name := range names {
		if strings.HasPrefix(name, collection+".") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// provenLive reports whether proof has a live test on any OS.
func provenLive(proof map[OS]Proof) bool {
	for _, p := range proof {
		if p.Status == ProofLive {
			return true
		}
	}
	return false
}

// percent is n over of as a percentage to one decimal place, 0 when of is 0.
func percent(n, of int) float64 {
	if of <= 0 {
		return 0
	}
	return math.Round(float64(n)*1000/float64(of)) / 10
}

// FloorFile is the ratchet every collection's percentages are held to, read
// from FloorFileName at the repository root.
type FloorFile struct {
	// Comment says what the file is, for a reader.
	Comment string `json:"_comment"`
	// Floors is, per collection, the least each percentage may be.
	Floors map[string]Floor `json:"floors"`
	// Exceptions records, per collection, why a floor was ever lowered. It is
	// a record for a reader: nothing compares a floor with its earlier value,
	// so a lowering is caught by review of this file's diff.
	Exceptions map[string]string `json:"_exceptions,omitempty"`
}

// Floor is one collection's least mapped and live percentages.
type Floor struct {
	Mapped float64 `json:"mapped"`
	Live   float64 `json:"live"`
}

// FloorFileName is the ratchet's file, at the repository root.
const FloorFileName = "coverage-floor-collections.json"

// Tolerance is how far under its floor a percentage may read before it is a
// drop, for rounding alone.
const Tolerance = 0.05

// LoadFloors reads root's FloorFileName strictly.
func LoadFloors(root string) (FloorFile, error) {
	var f FloorFile
	raw, err := os.ReadFile(filepath.Join(root, FloorFileName)) // #nosec G304 -- the repository's own ratchet file
	if err != nil {
		return f, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return f, fmt.Errorf("%s: %w", FloorFileName, err)
	}
	return f, nil
}

// Drops returns each way r reads under its floor in f, and whether f has a
// floor for r at all (internal/archtest fails a ledger with none).
func (f FloorFile) Drops(r Report) (drops []string, floored bool) {
	floor, ok := f.Floors[r.Collection]
	if !ok {
		return nil, false
	}
	if r.MappedPercent+Tolerance < floor.Mapped {
		drops = append(drops, fmt.Sprintf("%s: mapped %.1f%% is under its floor %.1f%%", r.Collection, r.MappedPercent, floor.Mapped))
	}
	if r.LivePercent+Tolerance < floor.Live {
		drops = append(drops, fmt.Sprintf("%s: live %.1f%% is under its floor %.1f%%", r.Collection, r.LivePercent, floor.Live))
	}
	return drops, true
}

// Orphans returns, in order, each collection f has a floor for that is not
// among collections (the ledgers that exist), so deleting or renaming a
// ledger cannot quietly take its floor out of the ratchet.
func (f FloorFile) Orphans(collections []string) []string {
	have := map[string]bool{}
	for _, c := range collections {
		have[c] = true
	}
	var out []string
	for _, c := range sortedKeys(f.Floors) {
		if !have[c] {
			out = append(out, c)
		}
	}
	return out
}
