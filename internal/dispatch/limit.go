// Package dispatch: a job's limit launch field, which of the job's devices
// its fan-out may dispatch, read with engine.ParseLimit, the matcher the CLI's
// --limit uses too, so a pattern means one thing on both tiers.
//
// A pattern matches a device's name, its tags, and every group it is in, its
// groups' ancestors included (inventory.GroupNamer). A device the limit
// excludes is not part of the job: it is passed over before admission and
// gets no row, which is how a device outside the job's inventory reads. A
// limit that admits none of the job's devices fails the job, so a typo does
// not run nothing and call it done. A rollback job is held outside the limit:
// it dispatches exactly the devices its plan undoes.
package dispatch

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// LimitField is the launch field naming which of a job's devices may run.
const LimitField = "limit"

// jobLimit is a job's limit, read once for its fan-out.
type jobLimit struct {
	limit  engine.Limit
	text   string
	groups map[pkginventory.DeviceID][]string
}

// limitOf reads job's limit and, when it has one, every device's group names.
// reason is set, and the limit zero, when the limit cannot be read; err is a
// failure to read the groups, which a redelivery may get past.
func (w *Worker) limitOf(ctx context.Context, job *Job) (l jobLimit, reason string, err error) {
	if job.RollbackOf != "" {
		return jobLimit{}, "", nil
	}
	text := job.Fields.String(LimitField)
	parsed, err := engine.ParseLimit(text)
	if err != nil {
		// ParseLimit's message names only the pattern, which the launcher
		// wrote, so it is safe on the job record.
		return jobLimit{}, err.Error(), nil
	}
	if parsed.IsZero() {
		return jobLimit{}, "", nil
	}
	l = jobLimit{limit: parsed, text: text}
	if namer, ok := w.repo.(inventory.GroupNamer); ok {
		if l.groups, err = namer.DeviceGroupNames(ctx); err != nil {
			return jobLimit{}, "", err
		}
	}
	return l, "", nil
}

// admits reports whether device is inside the limit, by name, tag or group.
func (l jobLimit) admits(device pkginventory.InventoryItem) bool {
	if l.limit.IsZero() {
		return true
	}
	tags := device.Tags()
	names := make([]string, 0, len(tags)+len(l.groups[device.ID()]))
	for _, t := range tags {
		names = append(names, string(t))
	}
	names = append(names, l.groups[device.ID()]...)
	return l.limit.Admits(device.Name(), names)
}

// noneMatched is the reason a job whose limit admitted none of its devices
// fails.
func (l jobLimit) noneMatched() string {
	return fmt.Sprintf("the launch's limit %q matches none of this job's devices", l.text)
}
