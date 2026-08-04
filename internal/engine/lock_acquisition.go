package engine

import (
	"encoding/json"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// AcquisitionStrategy selects when a task's device locks are acquired,
// mirroring PLAN.md Section 13's two acquisition strategies. The zero
// value is AcquisitionPerDeviceAsReached, so every existing runbook (no
// task sets Task.LockAcquisition) keeps today's only behavior unchanged.
//
// This is deliberately not the hierarchical system/inventory/group/device
// policy resolver PLAN.md Section 25 assigns to Phase 21: that resolver
// decides which strategy a runbook *should* use by default at each level
// of that hierarchy. This type only decides what happens once a task asks
// for one explicitly.
type AcquisitionStrategy int

const (
	// AcquisitionPerDeviceAsReached acquires each resolved device's lock
	// individually, immediately before running that device's action, and
	// releases it immediately after. Large fleets see minimal blocking,
	// but a lock held by another caller on one device does not stop this
	// task from running against its other, unblocked devices: partial
	// execution across the device set is possible.
	AcquisitionPerDeviceAsReached AcquisitionStrategy = iota

	// AcquisitionAllAtPlanTime acquires every resolved device's lock up
	// front, before any of this task's devices run, all-or-nothing
	// (lock.AcquireAll): if any single device is contended, the whole
	// node fails immediately with zero devices having run, rather than
	// partially executing. For changes that must never apply to only
	// some of their target devices.
	AcquisitionAllAtPlanTime
)

// String returns AcquisitionStrategy's name, used in error messages and logs.
func (s AcquisitionStrategy) String() string {
	switch s {
	case AcquisitionPerDeviceAsReached:
		return "per_device_as_reached"
	case AcquisitionAllAtPlanTime:
		return "all_at_plan_time"
	default:
		return "unknown"
	}
}

// ParseAcquisitionStrategy is String's inverse, for a runbook author's own
// YAML/JSON text (UnmarshalYAML/UnmarshalJSON below) and for any future
// adapter hydrating one from a stored string.
func ParseAcquisitionStrategy(s string) (AcquisitionStrategy, error) {
	switch s {
	case "per_device_as_reached":
		return AcquisitionPerDeviceAsReached, nil
	case "all_at_plan_time":
		return AcquisitionAllAtPlanTime, nil
	default:
		return 0, fmt.Errorf("unrecognized lock_acquisition value %q", s)
	}
}

// UnmarshalYAML decodes a runbook's own lock_acquisition: string (e.g.
// "all_at_plan_time") via ParseAcquisitionStrategy, so a runbook author
// never has to write the underlying int. Manager.Acquire's own tests and
// any other pure-Go construction of a Task still set this field directly
// as a typed constant; this method only matters for YAML decode.
func (s *AcquisitionStrategy) UnmarshalYAML(value *yaml.Node) error {
	var text string
	if err := value.Decode(&text); err != nil {
		return fmt.Errorf("lock_acquisition must be a string: %w", err)
	}
	parsed, err := ParseAcquisitionStrategy(text)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

// UnmarshalJSON is UnmarshalYAML's JSON counterpart, for a runbook authored
// or generated as JSON instead of YAML.
func (s *AcquisitionStrategy) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("lock_acquisition must be a string: %w", err)
	}
	parsed, err := ParseAcquisitionStrategy(text)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

// MarshalYAML is UnmarshalYAML's inverse, so a Task round-trips through
// YAML as the same human-readable string it was authored with.
func (s AcquisitionStrategy) MarshalYAML() (interface{}, error) {
	return s.String(), nil
}

// MarshalJSON is UnmarshalJSON's inverse.
func (s AcquisitionStrategy) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}
