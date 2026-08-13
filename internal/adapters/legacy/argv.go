package legacy

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// This file owns turning one dispatch's resolved launch fields into the
// real ansible-playbook argv adapter.go's Execute hands to a real
// container (orchestrator.go's own ContainerSpec.Argv), split out for the
// same file-per-concern reason inventory.go already is: adapter.go stays
// focused on sequencing, this file on the one command line every field
// AWX_PARITY_ROADMAP.md Section 3b.1 names has to reach.
//
// Before this file existed, Argv was a fixed literal
// ({"ansible-playbook", "-v", "-i", inventoryPath, playbookPath}): every
// field B1's per-field launch form let an author set (forks, limit,
// verbosity, timeout, job_tags, skip_tags, extra_vars) was captured onto
// the job record and the wire payload and then silently never read again,
// FAILURE_PATTERNS.md #116's own "correctly computed and never read by
// anything downstream" shape, one hop further down the chain.

// buildArgv builds the full ansible-playbook argument vector for one
// dispatch. fields is payload.Fields converted to launch.Fields (the two
// share an identical underlying type; see pkg/wire.DispatchPayload's own
// doc comment on Fields for why the wire type stays plain map[string]any).
// inventoryPath and playbookPath are the fixed in-container paths
// adapter.go's own constants name.
//
// Every flag is OMITTED, never emitted with a zero or empty value, when
// its field is absent: an unset "forks" must reach ansible-playbook as no
// --forks at all, letting Ansible's own default apply, not "--forks 0",
// which Ansible itself refuses. This mirrors launch.Fields' own sparse
// contract (internal/launch/fields.go): a key that is absent means "not
// supplied," never "supplied as the type's zero value."
//
// Flags are ordered before the positional playbook path (ansible-playbook
// accepts either order via argparse, but this is the conventional one and
// what every test in this package asserts against), except -e, which
// trails the playbook path: extra vars are the one value that can be
// arbitrarily large (a whole map, JSON-encoded), and keeping it last
// keeps every fixed-position flag's own index stable regardless of
// whether a launch supplied any.
func buildArgv(fields launch.Fields, extraVars map[string]any, inventoryPath, playbookPath string) ([]string, error) {
	argv := []string{"ansible-playbook"}

	if v := fields.Int("verbosity"); v > 0 {
		if v > 4 {
			v = 4
		}
		argv = append(argv, "-"+strings.Repeat("v", v))
	}

	argv = append(argv, "-i", inventoryPath)

	if limit := fields.String("limit"); limit != "" {
		argv = append(argv, "--limit", limit)
	}
	if forks := fields.Int("forks"); forks > 0 {
		argv = append(argv, "--forks", strconv.Itoa(forks))
	}
	if tags := fields.List("job_tags"); len(tags) > 0 {
		argv = append(argv, "--tags", strings.Join(tags, ","))
	}
	if skip := fields.List("skip_tags"); len(skip) > 0 {
		argv = append(argv, "--skip-tags", strings.Join(skip, ","))
	}

	argv = append(argv, playbookPath)

	if len(extraVars) > 0 {
		encoded, err := json.Marshal(extraVars)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal extra vars: %w", err)
		}
		argv = append(argv, "-e", string(encoded))
	}

	return argv, nil
}

// runTimeout returns the whole-run abandon deadline fields' own "timeout"
// field names, or zero when unset. Zero means "the caller wraps nothing":
// the playbook kind's own FieldSpec help text is explicit that zero is the
// platform default rather than an omission ("Seconds before the run is
// abandoned. Zero means no timeout."), and ansible-playbook itself has no
// flag for this (ANSIBLE_TIMEOUT is a per-connection dial timeout, not a
// whole-run one), so this is enforced by wrapping ctx around the
// orchestrator.Run call, not by an argv flag.
func runTimeout(fields launch.Fields) time.Duration {
	seconds := fields.Int("timeout")
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
