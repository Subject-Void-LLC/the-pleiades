package legacy

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
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
// trails the playbook path: it is the one flag whose presence depends on
// the launch rather than on the field set, and keeping it last keeps every
// fixed-position flag's own index stable regardless of whether a launch
// supplied any extra variables.
//
// # The argv leak this signature exists to make unrepresentable
//
// This function used to take the extra variables themselves and marshal
// them onto argv as a trailing `-e <json>`. That leaked every extra
// variable into the container's own process table: `ps auxww` shows it, any
// module that shells out can read /proc/<ppid>/cmdline, and a core dump
// contains it.
//
// Accepting the ephemeral container as the trust boundary (PLAN.md Section
// 29.4) does NOT rescue that, and the distinction matters: the customer's
// playbook runs INSIDE that boundary, so a credential injected for one
// module is readable by every other module in the same run. The boundary
// says a secret may cross into this container; it does not say every task
// in the container may read every other task's credentials.
//
// The fix is ansible-runner's own: write an extra-vars file and pass
// `-e @<path>`. hasExtraVars, a bool, is what makes the leak structurally
// impossible rather than merely fixed: no value reaches this function, so
// no value can reach argv, and a future edit here cannot reintroduce it
// without changing the signature.
//
// It is UNCONDITIONAL, not "only when a secret is present", for three
// independently sufficient reasons. A conditional means two code paths and
// the secret-carrying one is the rarely-exercised branch. This adapter
// cannot reliably know a variable is secret anyway: an operator can put a
// token in a plain extra_vars default with no credential involved. And
// `-e @file` also removes the unrelated ARG_MAX ceiling a large variable
// set would otherwise hit.
func buildArgv(fields launch.Fields, hasExtraVars bool, vaults []wire.InjectedVault, inventoryPath, playbookPath string) []string {
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

	// --vault-id names a PATH, never a password. The identifier is a label
	// an operator chose and the path is one this platform computed; neither
	// is a secret, which is exactly why the vault password reaches the run
	// as a file rather than as --vault-password.
	//
	// The unnamed default identity is written as a bare path rather than
	// "@path", because ansible-playbook reads a leading "@" as an empty
	// label and the two are not the same thing to it.
	for _, v := range vaults {
		if v.Identifier == "" {
			argv = append(argv, "--vault-id", v.Path)
			continue
		}
		argv = append(argv, "--vault-id", v.Identifier+"@"+v.Path)
	}

	argv = append(argv, playbookPath)

	if hasExtraVars {
		argv = append(argv, "-e", "@"+extraVarsContainerPath)
	}

	return argv
}

// encodeExtraVars renders the extra-variables file's bytes.
//
// JSON rather than YAML, deliberately. ansible-core sniffs an @-file's
// content and accepts either, but JSON is a strict subset of YAML 1.2 with
// no ambiguous scalars: a value like "yes", "no", "on" or "1.20" means one
// thing in JSON and something else to a YAML parser, and an extra variable
// silently becoming a boolean is the class of bug that ends with a task
// running against the wrong host. The release gate proves the pinned
// ansible-core reads this file identically to the `-e '<json>'` form it
// replaced, rather than assuming it.
func encodeExtraVars(vars map[string]any) ([]byte, error) {
	encoded, err := json.Marshal(vars)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal extra vars: %w", err)
	}
	return encoded, nil
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
