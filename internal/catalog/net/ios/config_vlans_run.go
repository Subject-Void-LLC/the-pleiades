// Package ios: what a run's lines did to a switch's VLAN database. The run
// decides what changed from the configuration's own differences and undoes
// through configure replace, and neither sees a VLAN a switch in VTP server
// or client mode keeps outside its configuration: a VLAN the lines made was
// reported unchanged and its undo never removed it. When the lines make or
// remove VLANs, the run reads the VLAN table before and after them.
package ios

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// vlanWatch is the VLAN table a run read before its lines. on says the
// lines touch VLANs; why, when the table could not be read, says why.
type vlanWatch struct {
	on     bool
	before map[int]vlan
	why    string
}

// watchVLANs reads the VLAN table before lines are applied, when they make
// or remove VLANs.
func watchVLANs(ctx context.Context, session iosSession, lines []string) (vlanWatch, error) {
	if !touchesVLANs(lines) {
		return vlanWatch{}, nil
	}
	before, why, err := readVLANTable(ctx, session)
	if err != nil {
		return vlanWatch{}, fmt.Errorf("%s: before the change: %w", configFQCN, err)
	}
	return vlanWatch{on: true, before: before, why: why}, nil
}

// vlanChange is one VLAN the lines changed: before or after is nil for a
// VLAN they made or removed.
type vlanChange struct {
	id            int
	before, after *vlan
}

// changes reads the VLAN table again and returns each VLAN that differs,
// in number order, leaving out the VLANs managed reports the configuration
// holds (a switch in VTP transparent mode, whose checkpoint returns them).
// why says the table could not be read before or after: what the lines did
// to the database is then not known.
func (w vlanWatch) changes(ctx context.Context, session iosSession, managed func(int) bool) ([]vlanChange, string, error) {
	if !w.on {
		return nil, "", nil
	}
	if w.before == nil {
		return nil, w.why, nil
	}
	after, why, err := readVLANTable(ctx, session)
	if err != nil {
		return nil, "", fmt.Errorf("%s: after the change: %w", configFQCN, err)
	}
	if after == nil {
		return nil, why, nil
	}
	ids := map[int]bool{}
	for id := range w.before {
		ids[id] = true
	}
	for id := range after {
		ids[id] = true
	}
	var out []vlanChange
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		b, hadB := w.before[id]
		a, hasA := after[id]
		if managed(id) || hadB && hasA && a == b {
			continue
		}
		c := vlanChange{id: id}
		if hadB {
			c.before = &b
		}
		if hasA {
			c.after = &a
		}
		out = append(out, c)
	}
	return out, "", nil
}

// vlanDifferences writes changes as IOS's contextual differences write a
// change: "+vlan 60" and the name under it for a VLAN made, "-vlan 70" for
// one removed, and "vlan 50" with its name or state lines for one changed.
func vlanDifferences(changes []vlanChange) []string {
	var out []string
	for _, c := range changes {
		head := fmt.Sprintf("vlan %d", c.id)
		switch {
		case c.before == nil:
			out = append(out, "+"+head)
			if !defaultName(c.id, c.after.name) {
				out = append(out, " +name "+c.after.name)
			}
		case c.after == nil:
			out = append(out, "-"+head)
			if !defaultName(c.id, c.before.name) {
				out = append(out, " -name "+c.before.name)
			}
		default:
			out = append(out, head)
			if c.before.name != c.after.name {
				if !defaultName(c.id, c.before.name) {
					out = append(out, " -name "+c.before.name)
				}
				if !defaultName(c.id, c.after.name) {
					out = append(out, " +name "+c.after.name)
				}
			}
			if c.before.state != c.after.state {
				out = append(out, " -state "+c.before.state, " +state "+c.after.state)
			}
		}
	}
	return out
}

// vlanUndo writes the lines that undo changes exactly: "no vlan N" for a
// VLAN the lines made, and the old name back for one they renamed. A VLAN
// they removed, or whose state they changed, cannot be put back exactly
// from show vlan brief, which shows neither its MTU, its type nor its
// private VLAN or remote SPAN settings: why then says so, and there is no
// undo.
func vlanUndo(changes []vlanChange) (lines []string, why string) {
	for _, c := range changes {
		switch {
		case c.before == nil:
			lines = append(lines, fmt.Sprintf("no vlan %d", c.id))
		case c.after == nil:
			return nil, fmt.Sprintf("VLAN %d was removed, and show vlan brief does not show all it held (its MTU, type, private VLAN or remote SPAN settings), so it cannot be made again exactly", c.id)
		case c.before.state != c.after.state:
			return nil, fmt.Sprintf("VLAN %d's state changed from %s to %s, which show vlan brief does not show exactly enough to set back", c.id, c.before.state, c.after.state)
		default:
			lines = append(lines, fmt.Sprintf("vlan %d", c.id))
			if defaultName(c.id, c.before.name) {
				lines = append(lines, " no name")
			} else {
				lines = append(lines, " name "+c.before.name)
			}
			lines = append(lines, "exit")
		}
	}
	return lines, ""
}

// managedInDifferences reports, from a run's contextual differences, the
// VLANs the configuration itself holds: a difference line at the top level
// that is "vlan N", with or without its sign.
func managedInDifferences(diffs []string) func(int) bool {
	held := map[string]bool{}
	for _, d := range diffs {
		if strings.HasPrefix(d, " ") {
			continue
		}
		held[strings.TrimSpace(strings.TrimLeft(d, "+-"))] = true
	}
	return func(id int) bool { return held[fmt.Sprintf("vlan %d", id)] }
}

// recordVLANUndo records the undo of a change that touched only the VLAN
// database: net.ios.config with the lines vlanUndo wrote, reaching the
// device the way the change did.
func recordVLANUndo(rc sdk.RunbookContext, params map[string]any, lines []string) error {
	undoLines := make([]any, len(lines))
	for i, l := range lines {
		undoLines[i] = l
	}
	undo := map[string]any{paramLines: undoLines}
	carryTransport(params, undo)
	return sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:        configFQCN,
		Params:      undo,
		Description: "Removes the VLANs this change made and puts back the names it changed, in the switch's VLAN database.",
	})
}

// vlanWarning says what a change's undo leaves of the VLAN database: the
// VLANs a replace to the checkpoint does not return, or why the database
// could not be read.
func vlanWarning(changes []vlanChange, why string) string {
	if why != "" {
		return why + ", so a VLAN the lines made or removed outside the configuration is not reported, and no undo returns it"
	}
	ids := make([]string, len(changes))
	for i, c := range changes {
		ids[i] = fmt.Sprint(c.id)
	}
	return fmt.Sprintf("the lines changed VLAN %s in the switch's VLAN database, which a return to the checkpoint does not put back, so the undo is partial", strings.Join(ids, ", "))
}
