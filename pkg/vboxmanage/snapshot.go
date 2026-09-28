// Snapshots: taking, restoring and deleting them, by UUID.
package vboxmanage

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Snapshot is one snapshot of a machine.
type Snapshot struct {
	Name        string
	UUID        string
	Description string
	// Path is where it sits in the tree: "" for the first snapshot, "-1"
	// for its first child, "-1-2" for that child's second, as VBoxManage
	// numbers them.
	Path string
}

// snapshotKey is how showvminfo names a snapshot's fields: SnapshotName,
// SnapshotName-1, SnapshotUUID-1-2, and so on.
var snapshotKey = regexp.MustCompile(`^Snapshot(Name|UUID|Description)((?:-[0-9]+)*)$`)

// snapshotsFrom reads every snapshot out of showvminfo's values, in the
// order VBoxManage lists them.
func snapshotsFrom(values Values) []Snapshot {
	var order []string
	byPath := map[string]*Snapshot{}
	for _, p := range values {
		m := snapshotKey.FindStringSubmatch(p.Key)
		if m == nil {
			continue
		}
		s, ok := byPath[m[2]]
		if !ok {
			s = &Snapshot{Path: m[2]}
			byPath[m[2]] = s
			order = append(order, m[2])
		}
		switch m[1] {
		case "Name":
			s.Name = p.Value
		case "UUID":
			s.UUID = p.Value
		case "Description":
			s.Description = p.Value
		}
	}
	snapshots := make([]Snapshot, 0, len(order))
	for _, path := range order {
		snapshots = append(snapshots, *byPath[path])
	}
	return snapshots
}

// SnapshotsNamed returns m's snapshots called name. VirtualBox lets two
// snapshots share a name, so there can be more than one.
func (m Machine) SnapshotsNamed(name string) []Snapshot {
	var found []Snapshot
	for _, s := range m.Snapshots {
		if s.Name == name {
			found = append(found, s)
		}
	}
	return found
}

// takenUUID is how snapshot take reports the snapshot it made.
var takenUUID = regexp.MustCompile(`Snapshot taken\. UUID: ([0-9a-fA-F-]{36})`)

// TakeSnapshot takes a snapshot of vm called name, and returns its UUID.
// It does not refuse a name the machine already has a snapshot under,
// because VirtualBox does not; a caller that needs names to be unique
// looks first (Machine.SnapshotsNamed).
func (h Host) TakeSnapshot(ctx context.Context, vm, name, description string) (string, error) {
	if err := CheckName("VM", vm); err != nil {
		return "", err
	}
	if err := CheckName("snapshot", name); err != nil {
		return "", err
	}
	if err := CheckDescription(description); err != nil {
		return "", err
	}
	args := []string{"snapshot", vm, "take", name}
	if description != "" {
		args = append(args, "--description", description)
	}
	out, err := h.run(ctx, args...)
	if err != nil {
		return "", err
	}
	m := takenUUID.FindStringSubmatch(out.Stdout)
	if m == nil {
		return "", fmt.Errorf("vboxmanage: snapshot take did not report the UUID it made: %q", strings.TrimSpace(out.Stdout))
	}
	return m[1], nil
}

// RestoreSnapshot puts vm back to the snapshot with uuid. The machine
// must not be running; its current state is lost.
func (h Host) RestoreSnapshot(ctx context.Context, vm, uuid string) error {
	return h.snapshotByUUID(ctx, vm, "restore", uuid)
}

// DeleteSnapshot deletes the snapshot of vm with uuid, merging its
// differencing disks into its children.
func (h Host) DeleteSnapshot(ctx context.Context, vm, uuid string) error {
	return h.snapshotByUUID(ctx, vm, "delete", uuid)
}

// uuidPattern is a snapshot or machine UUID.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// CheckUUID refuses a snapshot UUID that is not one, in VirtualBox's
// hyphenated form.
func CheckUUID(uuid string) error {
	if !uuidPattern.MatchString(uuid) {
		return fmt.Errorf("snapshot UUID %q is not a UUID", uuid)
	}
	return nil
}

// snapshotByUUID runs snapshot vm action uuid. A UUID rather than a name,
// because a name can match more than one snapshot.
func (h Host) snapshotByUUID(ctx context.Context, vm, action, uuid string) error {
	if err := CheckName("VM", vm); err != nil {
		return err
	}
	if err := CheckUUID(uuid); err != nil {
		return err
	}
	_, err := h.run(ctx, "snapshot", vm, action, uuid)
	return err
}

// CheckDescription refuses a snapshot description holding a line break or
// another control character, or longer than 200 characters: it travels
// as one command-line argument.
func CheckDescription(description string) error {
	if len(description) > 200 {
		return fmt.Errorf("snapshot description is %d characters, more than 200", len(description))
	}
	if strings.IndexFunc(description, unicode.IsControl) >= 0 {
		return fmt.Errorf("snapshot description holds a line break or another control character")
	}
	return nil
}
