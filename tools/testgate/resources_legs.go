// A lab leg's record as resources measurements: the memory and disk each
// package cost on the leg's machine, so the manifest packs a pass on the
// smallest machines the gate runs on (a 4 GB lab VM) by what they measured
// there, not by what a larger host with a warm build cache did.
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

// hostMeasured is one run's measurements and the GOOS/GOARCH that took them.
type hostMeasured struct {
	host string
	m    measured
}

// legMeasurements reads data as a lab leg's record (oscheck.LegRecord) and
// returns each of its runs' memory and disk by package, from the run's
// headroom, with the run's GOOS/GOARCH as its host; isLeg is false for a
// file of any other kind. A run with no headroom adds nothing, and a leg
// records no seconds by package. A divided package's own number stands for
// nothing, as in readMeasurements.
//
// A run's number for a package is the most the machine's memory fell while
// the package ran, so where packages ran together each is charged its
// neighbours' share too: a lab's numbers err high, which packs its next pass
// tighter, never looser.
func legMeasurements(data []byte, divided map[string]bool) (runs []hostMeasured, isLeg bool, err error) {
	var kind struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(data, &kind); err != nil {
		return nil, false, err
	}
	if kind.Kind != oscheck.LegKind {
		return nil, false, nil
	}
	rec, err := oscheck.ReadLegRecord(data)
	if err != nil {
		return nil, true, err
	}
	for _, run := range rec.Runs {
		if run.Headroom == nil {
			continue
		}
		if run.OS == "" || run.Arch == "" {
			return nil, true, fmt.Errorf("a run of the %s leg names no GOOS/GOARCH, so its numbers cannot say which machine they are", rec.Tool)
		}
		m := measured{seconds: map[string]float64{}, memory: map[string]float64{}, disk: map[string]float64{}}
		fold := func(dst, src map[string]float64) {
			for key, v := range src {
				key = strings.TrimPrefix(key, modulePrefix)
				if !divided[key] {
					dst[key] = v
				}
			}
		}
		fold(m.memory, run.Headroom.PackageMB)
		fold(m.disk, run.Headroom.PackageDiskMB)
		runs = append(runs, hostMeasured{host: run.OS + "/" + run.Arch, m: m})
	}
	return runs, true, nil
}
