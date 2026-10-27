// net.ios.checkpoints: the checkpoints net.ios.config left on a Cisco IOS
// device, listed, and with prune, the older ones deleted. A checkpoint holds
// the whole configuration, its secrets included, and stays on the device until
// the change's undo runs, so a device changed often fills its file system
// with them.
package ios

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// checkpointsFQCN is the method's name.
const checkpointsFQCN = "net.ios.checkpoints"

// The parameters.
const (
	paramPrune     = "prune"
	paramKeep      = "keep"
	paramOlderThan = "older_than"
)

// The stats.
const (
	statCheckpoints = "checkpoints"
	statDeleted     = "deleted"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: checkpointsFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh", "telnet", "console"},
			RequiredCapabilities: []capability.Name{capability.NameCiscoIOS},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			SupportsCheck:   true,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "A checkpoint deleted is gone, and the undo net.ios.config recorded for its change (net.ios.replace naming it) then refuses: prune only what no rollback will need. Listing changes nothing.",
			},
			Doc: checkpointsDoc(),
		},
		Invoke: Checkpoints,
		Check:  CheckCheckpoints,
	})
}

// checkpointsDoc is net.ios.checkpoints' reference documentation, which
// internal/forge/catalogdata holds an identical copy of.
func checkpointsDoc() collection.Doc {
	return collection.Doc{
		Summary: "Lists the checkpoints net.ios.config left on a Cisco IOS device, and with prune deletes the older ones.",
		Description: "Reads the file system net.ios.config keeps its checkpoints on (the device's default writable disk or flash, such as unix: or flash:) with dir, and reports each change's checkpoints, newest first: its id, the configuration before it and, for a change that completed, after it, with the time and size the device gives. Only files named as net.ios.config names them (pleiades-<12 hex digits>.cfg and -after.cfg) are ever listed or deleted, so a startup configuration or an image is never touched. " +
			"Each checkpoint holds the whole configuration, its secrets included, and stays on the device until its change's undo runs. With prune, the checkpoints of every change but the newest keep, or of every change older than older_than by the device's own clock (show clock, so a router whose clock was never set is measured against itself), or both (a change goes when either says so), are deleted with delete /force; a check reports what would go. A deleted checkpoint's undo (the net.ios.replace net.ios.config recorded) then refuses, so prune only changes no rollback will need, such as those of a lab about to be deleted.",
		Params: []collection.Param{
			{Name: paramPrune, Type: "bool", Default: "false", Description: "Delete the checkpoints keep and older_than select. Without it the method only lists."},
			{Name: paramKeep, Type: "int", Description: "With prune, keep the checkpoints of this many of the newest changes and delete the rest; 0 deletes every one."},
			{Name: paramOlderThan, Type: "string", Description: "With prune, delete the checkpoints of every change older than this, a Go duration such as 72h, measured by the device's own clock against the times it gives its files."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			{Name: sdk.ParamInsecureTelnet, Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."},
		},
		Returns: []collection.ReturnField{
			{Name: statCheckpoints, Type: "list of dict", Returned: "always", Description: "Each change's checkpoints, newest first: id, before, after (empty when the change did not complete), modified (RFC 3339, empty when the device's time did not read) and bytes."},
			{Name: statDeleted, Type: "list of string", Returned: "when prune is true", Description: "The files deleted, or in a check the files that would be."},
		},
		Examples: []collection.Example{
			{Name: "Keep the checkpoints of the last five changes", RunbookYAML: "- name: Prune old checkpoints\n  net.ios.checkpoints:\n    prune: true\n    keep: 5\n"},
		},
		SeeAlso: []string{"net.ios.config", "net.ios.replace"},
	}
}

// dirFile is one file a dir line names: its size, its time as the device
// gives it, and its name.
var dirFile = regexp.MustCompile(`^\s*\d+\s+\S+\s+(\d+)\s+([A-Z][a-z]{2}\s+\d{1,2}\s+\d{4}\s+\d{2}:\d{2}:\d{2}\s+[+-]\d{2}:\d{2})\s+(\S+)\s*$`)

// dirTime is the layout of a dir line's time, as IOL XE 17.15.1 prints it
// ("Oct 26 2026 01:16:16 +00:00", measured 2026-10-25).
const dirTime = "Jan 2 2006 15:04:05 -07:00"

// checkpointSet is one change's checkpoints.
type checkpointSet struct {
	id     string
	before string
	after  string
	// modified is the before file's time as the device gives it; wall is
	// the same clock reading with its offset dropped, which older_than
	// compares with the device's own clock (show clock), never this
	// machine's.
	modified time.Time
	wall     time.Time
	bytes    int64
}

// wallClock is t's clock reading as a time in UTC, its offset dropped.
func wallClock(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}

// showClock is IOS's show clock: an optional * (not set by NTP or a
// calendar) or . (set, unsynchronized), the time with its milliseconds, a
// zone, the weekday, the month, the day and the year ("*01:16:19.193 UTC
// Mon Oct 26 2026", IOL XE 17.15.1, 2026-10-25).
var showClock = regexp.MustCompile(`[*.]?(\d{1,2}:\d{2}:\d{2})(?:\.\d+)?\s+\S+\s+[A-Z][a-z]{2}\s+([A-Z][a-z]{2})\s+(\d{1,2})\s+(\d{4})`)

// deviceClock reads the device's own clock, as a wall-clock time.
func deviceClock(out string) (time.Time, bool) {
	m := showClock.FindStringSubmatch(out)
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.Parse("Jan 2 2006 15:04:05", m[2]+" "+m[3]+" "+m[4]+" "+m[1])
	if err != nil {
		return time.Time{}, false
	}
	return wallClock(t), true
}

// readCheckpoints lists the checkpoint sets on fileSystem from dir, newest
// first; a set whose time did not read sorts last.
func readCheckpoints(out, fileSystem string) []checkpointSet {
	byID := map[string]*checkpointSet{}
	for line := range strings.SplitSeq(out, "\n") {
		m := dirFile.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil || !mintedCheckpoint.MatchString(fileSystem+m[3]) {
			continue
		}
		name := fileSystem + m[3]
		id := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(m[3], "pleiades-"), ".cfg"), "-after")
		set := byID[id]
		if set == nil {
			set = &checkpointSet{id: id}
			byID[id] = set
		}
		size, _ := strconv.ParseInt(m[1], 10, 64)
		set.bytes += size
		when, err := time.Parse(dirTime, strings.Join(strings.Fields(m[2]), " "))
		if strings.HasSuffix(m[3], "-after.cfg") {
			set.after = name
		} else {
			set.before = name
			if err == nil {
				set.modified, set.wall = when, wallClock(when)
			}
		}
	}
	sets := make([]checkpointSet, 0, len(byID))
	for _, s := range byID {
		sets = append(sets, *s)
	}
	sort.SliceStable(sets, func(i, j int) bool {
		if sets[i].modified.Equal(sets[j].modified) {
			return sets[i].id < sets[j].id
		}
		return sets[i].modified.After(sets[j].modified)
	})
	return sets
}

// pruneSelect returns the sets prune deletes: every one past the newest keep
// (when keep is set), and every one older than olderThan (when set), now
// being the time to measure from.
func pruneSelect(sets []checkpointSet, keep int, keepSet bool, olderThan time.Duration, now time.Time) []checkpointSet {
	var out []checkpointSet
	for i, s := range sets {
		old := olderThan > 0 && !s.wall.IsZero() && now.Sub(s.wall) > olderThan
		if (keepSet && i >= keep) || old {
			out = append(out, s)
		}
	}
	return out
}

// Checkpoints implements the "net.ios.checkpoints" collection method.
func Checkpoints(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runCheckpoints(ctx, rc, device, params, collection.ModeExecute)
}

// CheckCheckpoints is its check: the same listing, and what prune would
// delete, deleting nothing.
func CheckCheckpoints(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runCheckpoints(ctx, rc, device, params, collection.ModeCheck)
}

// runCheckpoints lists, and with prune deletes unless mode is a check.
func runCheckpoints(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = checkpointsFQCN
	prune, err := sdk.BoolParamOr(params, paramPrune, false)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	keep, keepSet, err := sdk.IntParam(params, paramKeep)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	var olderThan time.Duration
	if raw := sdk.StringParam(params, paramOlderThan); raw != "" {
		if olderThan, err = time.ParseDuration(raw); err != nil || olderThan <= 0 {
			return collection.Result{}, fmt.Errorf("%s: %s %q is a positive Go duration, such as 72h", fqcn, paramOlderThan, raw)
		}
	}
	switch {
	case keepSet && keep < 0:
		return collection.Result{}, fmt.Errorf("%s: %s is 0 or more", fqcn, paramKeep)
	case prune && !keepSet && olderThan == 0:
		return collection.Result{}, fmt.Errorf("%s: %s needs %s, %s or both, to say which checkpoints go", fqcn, paramPrune, paramKeep, paramOlderThan)
	case !prune && (keepSet || olderThan > 0):
		return collection.Result{}, fmt.Errorf("%s: %s and %s apply only with %s: true", fqcn, paramKeep, paramOlderThan, paramPrune)
	}

	session, err := openSession(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = session.Close() }()

	fileSystem, err := checkpointFileSystem(ctx, session)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	out, err := session.Command(ctx, "dir "+fileSystem)
	if err != nil {
		return collection.Result{}, maskedError(fmt.Errorf("%s: %w", fqcn, err))
	}
	if rejected, refused := netcli.CommandRejection(out); refused {
		return collection.Result{}, fmt.Errorf("%s: dir %s: %s", fqcn, fileSystem, strings.TrimSpace(rejected))
	}
	sets := readCheckpoints(out, fileSystem)
	listed := make([]any, len(sets))
	for i, s := range sets {
		modified := ""
		if !s.modified.IsZero() {
			modified = s.modified.UTC().Format(time.RFC3339)
		}
		listed[i] = map[string]any{"id": s.id, "before": s.before, "after": s.after, "modified": modified, "bytes": s.bytes}
	}
	if err := rc.SetStat(statCheckpoints, listed); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !prune {
		return collection.Result{}, nil
	}

	// Age is the device's own: a router whose clock was never set dates
	// its files by that clock, and this machine's would make every one old.
	var now time.Time
	if olderThan > 0 {
		clock, err := session.Command(ctx, "show clock")
		if err != nil {
			return collection.Result{}, maskedError(fmt.Errorf("%s: %w", fqcn, err))
		}
		var ok bool
		if now, ok = deviceClock(clock); !ok {
			return collection.Result{}, fmt.Errorf("%s: the device's clock (show clock) did not read, so %s cannot be measured; prune by %s instead", fqcn, paramOlderThan, paramKeep)
		}
	}
	var deleted []string
	for _, s := range pruneSelect(sets, keep, keepSet, olderThan, now) {
		for _, path := range []string{s.after, s.before} {
			if path == "" {
				continue
			}
			if mode != collection.ModeCheck {
				if err := deleteFile(ctx, session, path); err != nil {
					_ = rc.SetStat(statDeleted, deleted)
					return collection.Result{Changed: len(deleted) > 0}, fmt.Errorf("%s: %w", fqcn, err)
				}
			}
			deleted = append(deleted, path)
		}
	}
	if err := rc.SetStat(statDeleted, deleted); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: len(deleted) > 0}, nil
}
