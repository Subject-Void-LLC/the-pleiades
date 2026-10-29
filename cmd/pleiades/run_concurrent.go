// Noticing another run changing the same devices during this one. Runs do
// not exclude each other (a failure injected while a change is verified is
// a deliberate second run), so nothing here refuses or waits: a run that
// changes devices records itself as active while it works, warns at its
// start about another live run on any of its devices, and warns at its end
// about any other run whose journal records a change to one of its devices
// while it worked. A check changes nothing, records nothing and warns about
// nothing.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The kinds of run warning, clireport's (it publishes them as the schema's
// known values).
const (
	warnConcurrentRun     = clireport.WarnConcurrentRun
	warnConcurrentChange  = clireport.WarnConcurrentChange
	warnConcurrentUnknown = clireport.WarnConcurrentUnknown
)

// runWarning is something about the run as a whole that the person running
// it has to know (clireport.RunWarning).
type runWarning = clireport.RunWarning

// concurrency is a changing run's record and what it needs at its end.
type concurrency struct {
	dir     string
	active  *journal.Active
	started time.Time
	ids     []string
	names   map[string]string
}

// beginConcurrency records the run as active on devices and returns the
// warnings for every other live run on any of them. It never fails the
// run: a record it cannot write is itself a warning.
func beginConcurrency(dir, label string, devices []pkginventory.InventoryItem) (*concurrency, []runWarning) {
	c := &concurrency{dir: dir, started: time.Now(), names: map[string]string{}}
	record := make([]journal.ActiveDevice, 0, len(devices))
	for _, d := range devices {
		id := string(d.ID())
		c.ids = append(c.ids, id)
		c.names[id] = d.Name()
		record = append(record, journal.ActiveDevice{ID: id, Name: d.Name()})
	}
	if len(record) == 0 {
		// A run that names no device (a project file written, a call with
		// no device) shares no device with any other.
		return c, nil
	}
	var warnings []runWarning
	active, err := journal.BeginActive(dir, journal.ActiveRun{Runbook: label, Devices: record})
	if err != nil {
		warnings = append(warnings, runWarning{Kind: warnConcurrentUnknown, Message: fmt.Sprintf("this run could not record itself as active, so another run will not notice it: %v", err)})
	} else {
		c.active = active
	}
	warnings = append(warnings, c.liveOthers("is changing")...)
	for i := range warnings {
		warnings[i].When = "start"
	}
	return c, warnings
}

// liveOthers is a warning for each other live run on this run's devices.
func (c *concurrency) liveOthers(verb string) []runWarning {
	self := ""
	if c.active != nil {
		self = c.active.ID
	}
	others, err := journal.OtherActive(c.dir, self, c.ids)
	if err != nil {
		return []runWarning{{Kind: warnConcurrentUnknown, Message: fmt.Sprintf("could not read which other runs are active: %v", err)}}
	}
	var warnings []runWarning
	for _, o := range others {
		var shared []string
		for _, d := range o.Devices {
			if _, ours := c.names[d.ID]; ours {
				shared = append(shared, termsafe.EscapeLine(d.Name))
			}
		}
		runbook := termsafe.EscapeLine(o.Runbook)
		warnings = append(warnings, runWarning{
			Kind:    warnConcurrentRun,
			Runbook: runbook,
			PID:     o.PID,
			Devices: shared,
			Message: fmt.Sprintf("another run %s %s (%s, pid %d on %s, started %s)", verb, strings.Join(shared, ", "), runbook, o.PID, termsafe.EscapeLine(o.Host), o.StartedAt.Local().Format(time.DateTime)),
		})
	}
	return warnings
}

// end removes the run's record and returns the warnings for every other
// run whose journal records a change to this run's devices since it
// started, and for any other run still active on them. self is this run's
// own id, whose journal is not another run's.
func (c *concurrency) end(self string) []runWarning {
	if c == nil || len(c.ids) == 0 {
		return nil
	}
	var warnings []runWarning
	changes, unread, err := journal.ChangesDuring(c.dir, self, c.ids, c.started, time.Now())
	if err != nil {
		warnings = append(warnings, runWarning{Kind: warnConcurrentUnknown, Message: fmt.Sprintf("could not read the journals to see whether another run changed this run's devices: %v", err)})
	}
	for _, ch := range changes {
		shared := make([]string, 0, len(ch.DeviceIDs))
		for _, id := range ch.DeviceIDs {
			shared = append(shared, c.names[id])
		}
		what := "another run"
		if ch.RollbackOf != "" {
			what = "a rollback of " + ch.RollbackOf
		}
		runbook := termsafe.EscapeLine(ch.Runbook)
		warnings = append(warnings, runWarning{
			Kind:    warnConcurrentChange,
			RunID:   ch.RunID,
			Runbook: runbook,
			Devices: shared,
			Message: fmt.Sprintf("%s changed %s while this run worked: run %s (%s), %d changed task(s); `pleiades journal show %s` lists them", what, strings.Join(shared, ", "), ch.RunID, runbook, ch.Changes, ch.RunID),
		})
	}
	for _, id := range unread {
		warnings = append(warnings, runWarning{Kind: warnConcurrentUnknown, RunID: id, Message: fmt.Sprintf("could not read run %s's journal, so whether it changed this run's devices is not known", id)})
	}
	warnings = append(warnings, c.liveOthers("is still changing")...)
	c.release()
	for i := range warnings {
		warnings[i].When = "end"
	}
	return warnings
}

// release removes the run's record, once; every path out of a changing
// run reaches it, the ones that fail before the run starts included.
func (c *concurrency) release() {
	if c == nil || c.active == nil {
		return
	}
	if err := c.active.End(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: removing this run's active record: %v\n", err)
	}
	c.active = nil
}

// printRunWarnings prints the warnings of rep given when, one line each.
func printRunWarnings(rep *runReport, when string) {
	for _, w := range rep.Warnings {
		if w.When == when {
			fmt.Printf("WARNING: %s\n", termsafe.EscapeLine(w.Message))
		}
	}
}

// hasWarnings reports whether rep holds a warning given when.
func hasWarnings(rep *runReport, when string) bool {
	for _, w := range rep.Warnings {
		if w.When == when {
			return true
		}
	}
	return false
}
