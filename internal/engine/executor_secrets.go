package engine

import (
	"fmt"
	"strings"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// minMaskableSecretLength is the shortest string value markRegisterMask or
// applySecretMask will accept as a secret.
//
// It is redact.MinLiteralLength rather than a second copy of the number.
// This constant and that one answer the identical question, "is this value
// long enough to substring-mask safely", and PLAN.md Section 25 allows the
// masking ruleset one implementation. Phase 22 moved the reasoning to
// redact.MinLiteralLength's own doc comment, where the algorithm it
// protects now lives; the short version is that scrubbing a short or common
// value corrupts unrelated output for the rest of the run, which is worse
// than not masking at all.
const minMaskableSecretLength = redact.MinLiteralLength

// secretMaskValue validates that v is safe to add to a run's secret set,
// returning the string to mask. field is used only to name the problem in
// an error; the error text never includes v itself, since a value that
// fails this check must not leak into an error message.
func secretMaskValue(field string, v interface{}) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q is a %T, not a string: only string values can be marked secret", field, v)
	}
	if len(s) < minMaskableSecretLength {
		return "", fmt.Errorf("field %q is %d byte(s), shorter than the %d-byte minimum required to mark a value secret", field, len(s), minMaskableSecretLength)
	}
	return s, nil
}

// stringSet is a concurrency-safe set of strings, used for a run's
// accumulated secret values (r.secrets) and the register names a
// "set_metadata" task populated (r.metadataRegisters). It mirrors
// inProcessWorkflowContext's own mutex-guarded shape and is scoped to
// exactly one Run call: a fresh stringSet is constructed inside Run, never
// stored on the long-lived Executor, so values from one run can never leak
// into a later Run call on a reused Executor.
type stringSet struct {
	mu sync.RWMutex
	m  map[string]struct{}
}

// newStringSet returns an empty stringSet ready for concurrent use.
func newStringSet() *stringSet {
	return &stringSet{m: make(map[string]struct{})}
}

// Add records v in the set. A repeat Add of the same value is a no-op.
func (s *stringSet) Add(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[v] = struct{}{}
}

// Snapshot returns every value currently in the set, in no particular
// order (a set has none), safe to use after the mutex is released.
func (s *stringSet) Snapshot() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.m))
	for v := range s.m {
		out = append(out, v)
	}
	return out
}

// resolveRegisterMaskPath walks path (dot-separated) through stats,
// descending into nested map[string]interface{} values one segment at a
// time. It returns (value, true, nil) if the full path resolves;
// (nil, false, nil) if any segment is simply absent, the same benign-skip
// treatment a top-level-only field gets (not every action produces every
// field a task might ask to protect); and a non-nil error if the path
// tries to descend through a segment that resolved to something present
// but not itself a map, since there is nowhere left to go and silently
// stopping would leave the author believing a value is protected when it
// is not.
func resolveRegisterMaskPath(stats map[string]interface{}, path string) (interface{}, bool, error) {
	segments := strings.Split(path, ".")
	var current interface{} = stats
	for i, seg := range segments {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil, false, fmt.Errorf("path %q: %q is not a map, cannot resolve %q", path, strings.Join(segments[:i], "."), strings.Join(segments[i:], "."))
		}
		v, ok := m[seg]
		if !ok {
			return nil, false, nil
		}
		current = v
	}
	return current, true, nil
}

// markRegisterMask marks cmd.Task.RegisterMask's values, read from
// actionResult.Stats (this task's own just-computed result), as secret.
// Each entry is a dotted path (resolveRegisterMaskPath), so a top-level
// field and a nested one use the same syntax. A path may optionally be
// written with this task's own Register name as its leading segment
// (Task.RegisterMask's own doc comment, dag.go), mirroring when_cel's
// stat.<register> addressing; that exact prefix, if present, is stripped
// before resolving into Stats, which itself has no register-name key at
// all (Stats is the flat result map, never nested under its own register
// name), so leaving the prefix on would silently resolve to nothing. A
// path absent from Stats (after stripping, if applicable) is a benign
// skip: not every action produces every field a task might ask to
// protect. A path that resolves to something present but invalid (not a
// long-enough string, see secretMaskValue, or blocked partway through by
// a non-map intermediate value) is a hard error, since silently ignoring
// it would leave the author believing a value is protected when it is
// not.
func (r *run) markRegisterMask(cmd nodeExecution, actionResult ActionResult) error {
	for _, path := range cmd.Task.RegisterMask {
		resolvePath := path
		if cmd.Task.Register != "" {
			if rest, ok := strings.CutPrefix(path, cmd.Task.Register+"."); ok {
				resolvePath = rest
			}
		}

		v, found, err := resolveRegisterMaskPath(actionResult.Stats, resolvePath)
		if err != nil {
			return fmt.Errorf("register_mask: %w", err)
		}
		if !found {
			continue
		}
		s, err := secretMaskValue(path, v)
		if err != nil {
			return fmt.Errorf("register_mask: %w", err)
		}
		r.secrets.Add(s)
	}
	return nil
}

// applySecretMask marks task.SecretMask's fields, read from every device
// currently present under task.SecretMask.Register in WorkflowContext, as
// secret. It runs once per node (runNode), not once per resolved device
// (runOne): SecretMask does not depend on which device this task itself
// targets, it reaches back to an earlier register's data across every
// device that register has, matching "wherever they are" rather than one
// specific device.
//
// A Register with no entry at all in WorkflowContext (never produced, or
// produced by a task that has not run yet, or whose own condition made it
// skip) is a hard error naming the register, never a value, mirroring
// resolveDevices' own "a non-empty target that resolves to no device is an
// error" precedent (action.go). A Register that exists but is missing a
// requested field on one specific device is benign, not every device
// necessarily produced every field, mirroring the same top-level-absence-
// is-an-error-but-per-item-absence-is-not distinction resolveDevices draws
// between "target resolves to zero devices" and everything downstream of
// that.
//
// Masked values are only ever added to r.secrets, an output-boundary
// concern (run.publish, and a caller's own printed output via
// RunResult.Secrets): they are never written back into WorkflowContext, so
// a later when_cel condition still evaluates against the real, unmasked
// value. Corrupting that would silently break branching logic.
func (r *run) applySecretMask(task *Task) error {
	if task.SecretMask == nil {
		return nil
	}

	stat, err := r.x.workflow.Read()
	if err != nil {
		return fmt.Errorf("secret_mask: failed to read workflow context: %w", err)
	}

	byDevice, ok := stat[task.SecretMask.Register].(map[string]interface{})
	if !ok || len(byDevice) == 0 {
		return fmt.Errorf("secret_mask: register %q has no recorded result yet: it must be registered by an earlier task that has already run", task.SecretMask.Register)
	}

	for deviceID, raw := range byDevice {
		deviceStats, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		for _, field := range task.SecretMask.Fields {
			v, ok := deviceStats[field]
			if !ok {
				continue
			}
			s, err := secretMaskValue(field, v)
			if err != nil {
				return fmt.Errorf("secret_mask: register %q, device %q: %w", task.SecretMask.Register, deviceID, err)
			}
			r.secrets.Add(s)
		}
	}
	return nil
}
