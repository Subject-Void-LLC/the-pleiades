// Package ios implements "net.ios.config": the Cisco-IOS-specific
// configuration method, built on netcli.IOS's own real paging,
// configuration-mode and error conventions rather than a device's
// declared cli_prompt property. internal/catalog/net/cli's generic
// "net.cli.*" siblings are what a device with no vendor-specific method
// yet falls back to.
package ios

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// paramLines and paramBackup match cisco.ios.ios_config's own parameter
// names, the same reuse-Ansible's-vocabulary reasoning
// internal/catalog/exec and internal/catalog/net/cli both already
// follow.
const (
	paramLines  = "lines"
	paramBackup = "backup"
)

// statBackup is the stat name backup: true records the pre-change
// running-config under.
const statBackup = "backup"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "net.ios.config",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
			},
			RequiredCapabilities: []capability.Name{
				capability.NameCiscoIOS,
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			// IOS's common "no <line>" negation convention is not
			// reliable enough across every configuration statement (a
			// route-map or an ACL entry do not always negate cleanly
			// with a bare "no" prefix) to assert as this platform's own
			// claim, the same carefulness
			// internal/catalog/svc/windows's enable/disable inverse
			// logic already applies rather than asserting an inverse
			// broadly.
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "IOS's \"no <line>\" negation convention does not reliably invert every configuration statement (a route-map or ACL entry, for instance), so this platform does not assert an inverse it cannot guarantee. Set backup: true to capture the prior running-config for a human-directed rollback.",
			},
			NoCheckReason: "what a configuration line changes is decided by IOS's own parser as it applies the line, and IOS " +
				"offers no way to ask without applying it",
			Doc: configDoc(),
		},
		Invoke: Config,
	})
}

func configDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Applies configuration lines to a Cisco IOS device, with an optional pre-change backup.",
		Description: "Opens an interactive PTY session over SSH, using netcli.IOS's own real paging, configuration-mode and error conventions (verified directly against a real Cisco IOS XE device, not assumed), and applies lines as a batch: \"configure terminal\", each line in order, then \"end\". Aborts on the first line the device rejects (a real IOS \"% ...\" error), still leaving configuration mode before returning that error. When backup is true, runs \"show running-config\" before applying anything and records it under the backup stat, giving an operator something to restore from by hand; this platform does not attempt an automatic rollback (see this method's own Reversibility notes for why). Reports changed whenever every line reaches the device without error: a configuration line's effect cannot be inspected before it runs, the same reasoning exec.command and net.cli.command both apply.",
		Params: []collection.Param{
			{Name: paramLines, Type: "list of string", Required: true, Description: "The configuration lines to apply, in order, WITHOUT \"configure terminal\" or \"end\": this method supplies both itself.", Format: collection.ParamFormatCommand},
			{Name: paramBackup, Type: "bool", Default: "false", Description: "Capture the device's running-config with \"show running-config\" before applying any line, recorded under the backup stat."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statBackup, Type: "string", Returned: "when backup is true", Description: "The device's full running-config, captured immediately before this task's own lines were applied. Not sanitized: it genuinely contains this device's own enable secret, enable password, local user password hashes, and any TACACS+/RADIUS shared key in whatever strength (or weakness -- IOS's own \"type 7\" is trivially reversible) encoding the device applies. Mask it with \"register_mask: backup\" on this task."},
		},
		Examples: []collection.Example{
			{
				Name:        "Create a loopback interface with a backup",
				RunbookYAML: "- name: Add a loopback interface\n  net.ios.config:\n    backup: true\n    lines:\n      - interface Loopback0\n      - description managed by pleiades\n",
			},
		},
		SeeAlso: []string{"net.cli.command", "net.cli.config"},
	}
}

// Config implements the "net.ios.config" collection method.
func Config(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "net.ios.config"

	lines, present, err := sdk.StringSlice(params, paramLines)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !present || len(lines) == 0 {
		return collection.Result{}, fmt.Errorf("%s: %s is required and must be a non-empty list", fqcn, paramLines)
	}

	backup, err := sdk.BoolParamOr(params, paramBackup, false)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	session, err := openSession(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = session.Close() }()

	if backup {
		out, err := session.Command(ctx, "show running-config")
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: capturing backup: %w", fqcn, err)
		}
		if err := rc.SetStat(statBackup, out); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	if err := session.Config(ctx, lines); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	return collection.Result{Changed: true}, nil
}

// iosSession is the narrow surface Config actually uses from a
// *netcli.Session: enough that ios_internal_test.go can swap in a
// canned double with no real I/O, and no more.
type iosSession interface {
	Command(ctx context.Context, line string) (string, error)
	Config(ctx context.Context, lines []string) error
	Close() error
}

// openSession is the seam this package's own tests swap, the same role
// internal/catalog/net/cli's own openSession plays there -- a separate
// package, so a separate small seam rather than a shared one.
var openSession = realOpenSession

func realOpenSession(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (iosSession, error) {
	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return nil, err
	}

	shell, err := conn.Shell(ctx, remoteexec.ShellOptions{})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}

	session, err := netcli.Open(ctx, shell, netcli.IOS, netcli.Options{})
	if err != nil {
		_ = shell.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}

	return &sessionAndConn{Session: session, conn: conn}, nil
}

// sessionAndConn embeds a *netcli.Session (promoting Command and
// Config) but overrides Close so one call releases the Session's own
// PTY session AND the underlying *remoteexec.Conn it was opened on. See
// internal/catalog/net/cli's identically-named, independently-declared
// type for the full reasoning.
//
// This type and the openSession seam above are both duplicated rather
// than shared, for a reason the build enforces: a Collection package
// may import only pkg/ (internal/archtest's
// TestCatalogPackagesImportOnlyPkg), so these two packages cannot share
// an internal/ helper, and moving the seam into pkg/netcli would export
// test scaffolding into a public API surface.
type sessionAndConn struct {
	*netcli.Session
	conn *remoteexec.Conn
}

func (s *sessionAndConn) Close() error {
	sessErr := s.Session.Close()
	connErr := s.conn.Close()
	if sessErr != nil {
		return sessErr
	}
	return connErr
}
