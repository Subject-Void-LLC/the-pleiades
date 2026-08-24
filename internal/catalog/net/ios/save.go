package ios

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "net.ios.save",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("CiscoIOSCapable"),
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
			},
			// TODO(forge): PlatformTargets narrows this manifest to a
			// specific vendor, model, firmware range, or deployment
			// context (pkg/collection.PlatformTarget). Left nil: thin
			// flag parsing only, per Phase 33's own checklist. Add real
			// entries by hand once this method's platform scope is known.
			PlatformTargets: nil,
			EngineVersion:   ">=1.0.0",
			Status:          collection.StatusImplemented,
			// Saving overwrites startup-config with running-config.
			// The configuration that startup-config held before this ran
			// is gone from the device at that moment, and this platform
			// never had a copy of it, so no inverse can be derived from
			// the forward run the way sdk.RecordInverse expects.
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "Saving overwrites startup-config with running-config, and the device keeps no copy of what startup-config held beforehand, so this platform cannot reconstruct an inverse. Capture the prior configuration first (net.ios.config's backup parameter) if a rollback target is needed.",
			},
			Doc: collection.Doc{
				Summary:     "Saves a Cisco IOS device's running configuration to startup.",
				Description: "Runs IOS's \"write memory\", copying running-config over startup-config so the current configuration survives a reload. This is the step that makes every earlier net.ios.config task permanent, and it is deliberately a separate method rather than a parameter on net.ios.config: persisting configuration is a decision about blast radius, not a detail of applying a line, and a runbook that applies several changes should be able to decide once, at the end, whether any of them should outlive the next reload. Reports changed whenever the save completes, since IOS gives no way to know whether startup-config already matched. Aborts on a real IOS \"% ...\" error rather than reporting a save that did not happen.",
				Params: []collection.Param{
					{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				},
				Returns: []collection.ReturnField{
					{Name: "stdout", Type: "string", Returned: "always", Description: "Whatever the device printed in response to \"write memory\", typically a \"Building configuration...\" line followed by \"[OK]\"."},
				},
				Examples: []collection.Example{
					{Name: "Persist a change after verifying it", RunbookYAML: "- name: Save the running configuration\n  net.ios.save:\n"},
				},
				SeeAlso: []string{"net.ios.config"},
			},
		},
		Invoke: Save,
	})
}

// statSaveStdout is the stat name this method records the device's own
// reply under.
const statSaveStdout = "stdout"

// saveCommand is IOS's own abbreviation-free spelling of "copy
// running-config startup-config". "write memory" is used rather than the
// copy form deliberately: the copy form prompts for a destination
// filename on a real device, and this method's session has no way to
// answer an interactive prompt it did not expect.
const saveCommand = "write memory"

// Save implements the "net.ios.save" collection method.
func Save(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "net.ios.save"

	session, err := openSession(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = session.Close() }()

	out, err := session.Command(ctx, saveCommand)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	// netcli.Session.Command reports transport failures but does not
	// inspect the dialect's error convention; only Config does. Without
	// this check a device that refused the save (no privilege, a full
	// filesystem) would be reported as a successful, changed save, which
	// is the worst available outcome for a method whose entire purpose is
	// making a change permanent.
	if netcli.IOS.ErrorPattern.MatchString(out) {
		return collection.Result{}, fmt.Errorf("%s: device rejected %q: %s", fqcn, saveCommand, strings.TrimSpace(firstErrorLine(out)))
	}

	if err := rc.SetStat(statSaveStdout, out); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	// Reported changed unconditionally on success. IOS offers no way to
	// ask whether startup-config already matched running-config, so
	// claiming "no change" would be a guess, and the convention this
	// repository already settled on (exec.command, net.cli.command,
	// net.ios.config) is that an uninspectable effect reports changed.
	return collection.Result{Changed: true}, nil
}
