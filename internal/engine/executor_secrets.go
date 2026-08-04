package engine

import (
	"fmt"
	"sync"
)

// minMaskableSecretLength is the shortest string value markSecretFields or
// applySecretMask will accept as a secret. credential.Mask has no minimum
// length of its own: it substring-scrubs whatever it is given, anywhere it
// appears. A short or common value (a bool stringified to "true", a
// one-digit exit code) added to the mask set would scrub that substring out
// of every later message and printed line for the rest of the run,
// corrupting unrelated output, which is worse than not masking at all. 8 is
// a policy choice, not a derived number: it matches
// credential.maskPlaceholder's own width (a value shorter than what would
// replace it hides nothing meaningful) and a conventional minimum password
// length. A genuinely short real secret (a 4-6 digit PIN) cannot be safely
// substring-masked by this mechanism at all, ever, regardless of this
// guard; that limitation is inherent to substring masking, not introduced
// by this check.
const minMaskableSecretLength = 8

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

// markSecretFields marks cmd.Task.SecretFields' values, read from
// actionResult.Stats (this task's own just-computed result), as secret.
// A field named in SecretFields but absent from Stats is a benign skip:
// not every action produces every field a task might ask to protect. A
// present-but-invalid field (not a long-enough string, see
// secretMaskValue) is a hard error, since silently ignoring it would leave
// the author believing a value is protected when it is not.
func (r *run) markSecretFields(cmd nodeExecution, actionResult ActionResult) error {
	for _, field := range cmd.Task.SecretFields {
		v, ok := actionResult.Stats[field]
		if !ok {
			continue
		}
		s, err := secretMaskValue(field, v)
		if err != nil {
			return fmt.Errorf("secret_fields: %w", err)
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
