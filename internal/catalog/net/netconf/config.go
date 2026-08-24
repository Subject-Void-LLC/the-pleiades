// Package netconf implements "net.netconf.config": vendor-neutral
// configuration over RFC 6241 NETCONF, built on pkg/netconf's client and
// reached over pkg/remoteexec's SSH subsystem channel.
//
// It is the structured-data sibling of internal/catalog/net/cli's
// "net.cli.*" and internal/catalog/net/ios's "net.ios.config". Those
// drive an interactive terminal and send lines a human would type; this
// one sends an XML document against a named datastore and gets a
// machine-readable answer, including which element the device rejected
// and why. Both reach the same Cisco device; which is appropriate is a
// question about the change, not about the platform.
package netconf

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameter names come from ansible.netcommon.netconf_config rather than
// being coined here, matching this project's standing rule that a
// runbook feature reuses Ansible's vocabulary instead of inventing a new
// one. content, target, default_operation, error_option, lock, commit
// and backup are all that module's own names, with the same meanings.
const (
	paramContent          = "content"
	paramTarget           = "target"
	paramDefaultOperation = "default_operation"
	paramErrorOption      = "error_option"
	paramLock             = "lock"
	paramCommit           = "commit"
	paramBackup           = "backup"
)

// statBackup is the stat name backup: true records the pre-change
// configuration under.
const statBackup = "backup"

// The three values the lock parameter accepts, matching
// ansible.netcommon.netconf_config's own.
const (
	lockNever       = "never"
	lockAlways      = "always"
	lockIfSupported = "if_supported"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "net.netconf.config",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"netconf",
			},
			RequiredCapabilities: []capability.Name{
				capability.NameNetconf,
				// SSHTransportCapable is declared alongside it, not
				// implied by it. NETCONF is carried over an SSH
				// subsystem channel, so sdk.Connect asserts this
				// interface at run time regardless; declaring it moves
				// that refusal to plan time, where "pleiades validate"
				// can report it before anything dials.
				capability.NameSSHTransport,
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=1.0.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "An edit-config merge has no reliable inverse: deleting what was added is not the same as restoring what was replaced, and this method cannot know which of the two a given element did. Set backup: true to capture the prior configuration for a human-directed rollback. Note that rollback-on-error, which this method requests whenever the device supports it, covers a DIFFERENT case: it undoes a partially applied edit that the device itself rejected, not one that succeeded and was later regretted.",
			},
			Doc: configDoc(),
		},
		Invoke: Config,
	})
}

func configDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Applies a configuration document to a device over NETCONF, with an optional pre-change backup.",
		Description: "Opens an RFC 6241 NETCONF session over the SSH \"netconf\" subsystem and applies content to the target datastore with edit-config. Parameter names are ansible.netcommon.netconf_config's own. The session negotiates RFC 6242 chunked framing whenever the device offers base:1.1, and requests rollback-on-error whenever the device advertises it, so a rejected document leaves the device unchanged rather than half configured; that matters most on a device offering only writable-running, which is what Cisco IOS XE offers, because such a device has no staging area and every element lands on the live configuration as it is applied. A datastore the device never advertised support for is refused when the session opens rather than at the first write, naming the missing capability. Unlike the net.cli.* and net.ios.config methods, a rejected element comes back as a structured error carrying the device's own error-tag and the XPath of the element it objected to. Reports changed whenever the document reaches the device and the device answers ok.",
		Params: []collection.Param{
			{Name: paramContent, Type: "string", Required: true, Description: "The configuration document to apply, as the XML that goes inside edit-config's <config> element. Per-element operations are expressed the standard way, with an nc:operation attribute; pair that with default_operation: none so the device changes only what the document explicitly names."},
			{Name: paramTarget, Type: "string", Default: "running", Description: "The datastore to configure: running, candidate or startup. A datastore the device does not advertise support for is refused before anything is applied. Cisco IOS XE offers only running."},
			{Name: paramDefaultOperation, Type: "string", Default: "merge", Description: "What the device does with elements carrying no explicit operation attribute: merge, replace or none. RFC 6241 defines no \"delete\" here; express a delete with an nc:operation attribute in content."},
			{Name: paramErrorOption, Type: "string", Default: "rollback-on-error when the device supports it, otherwise the device's own stop-on-error default", Description: "How the device handles a rejected element: stop-on-error, continue-on-error or rollback-on-error. Left unset this method asks for rollback-on-error whenever the device advertises the capability, because stop-on-error leaves a rejected document half applied."},
			{Name: paramLock, Type: "string", Default: "never", Description: "Whether to lock the target datastore for the duration: never, always, or if_supported. Locking prevents another client changing the datastore mid-edit; it also blocks every other client, which matters on a shared device."},
			{Name: paramCommit, Type: "bool", Default: "true", Description: "Commit after a successful edit. Only meaningful when target is candidate, since a running-datastore edit is already live; ignored otherwise."},
			{Name: paramBackup, Type: "bool", Default: "false", Description: "Capture the target datastore's full contents with get-config before applying anything, recorded under the backup stat."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statBackup, Type: "string", Returned: "when backup is true", Description: "The target datastore's full contents as XML, captured immediately before this task's own document was applied. Not sanitized: a device's configuration genuinely contains its enable secret, local user password hashes, and any TACACS+/RADIUS shared key, in whatever strength of encoding the device applies. Mask it with \"register_mask: backup\" on this task."},
		},
		Examples: []collection.Example{
			{
				Name:        "Set a device's hostname over NETCONF",
				RunbookYAML: "- name: Set the hostname\n  net.netconf.config:\n    content: |\n      <native xmlns=\"http://cisco.com/ns/yang/Cisco-IOS-XE-native\">\n        <hostname>edge-01</hostname>\n      </native>\n    backup: true\n  register_mask: backup\n",
			},
			{
				Name:        "Remove an interface, changing nothing else",
				RunbookYAML: "- name: Remove the loopback\n  net.netconf.config:\n    default_operation: none\n    content: |\n      <native xmlns=\"http://cisco.com/ns/yang/Cisco-IOS-XE-native\">\n        <interface>\n          <Loopback xmlns:nc=\"urn:ietf:params:xml:ns:netconf:base:1.0\" nc:operation=\"delete\">\n            <name>8990</name>\n          </Loopback>\n        </interface>\n      </native>\n",
			},
		},
		SeeAlso: []string{"net.ios.config", "net.ios.save", "net.cli.config"},
	}
}

// Config implements the "net.netconf.config" collection method.
func Config(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "net.netconf.config"

	content, err := sdk.RequiredStringParam(params, paramContent)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if strings.TrimSpace(content) == "" {
		return collection.Result{}, fmt.Errorf("%s: %s is required and must not be empty", fqcn, paramContent)
	}

	target, err := targetParam(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	lock, err := lockParam(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	backup, err := sdk.BoolParamOr(params, paramBackup, false)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	commit, err := sdk.BoolParamOr(params, paramCommit, true)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	session, err := openSession(ctx, rc, device, params, fqcn, target)
	if err != nil {
		return collection.Result{}, err
	}
	// Closed without the polite <close-session/> RPC, because that RPC
	// would need a context and this defer runs on the error path too,
	// where the context may already be done. Close on the underlying
	// stream is what actually releases the SSH channel.
	defer func() { _ = session.Close() }()

	if lock == lockAlways || (lock == lockIfSupported && session.HasCapability(netconf.CapabilityCandidate)) {
		if err := session.Lock(ctx); err != nil {
			if lock == lockAlways {
				return collection.Result{}, fmt.Errorf("%s: locking the %s datastore: %w", fqcn, target, err)
			}
		} else {
			defer func() { _ = session.Unlock(ctx) }()
		}
	}

	if backup {
		payload, err := session.GetConfig(ctx, datastore.Path{})
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: capturing backup: %w", fqcn, err)
		}
		if err := rc.SetStat(statBackup, string(payload.Bytes)); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	req := netconf.EditConfigRequest{
		Config:           content,
		DefaultOperation: sdk.StringParam(params, paramDefaultOperation),
		ErrorOption:      sdk.StringParam(params, paramErrorOption),
	}
	if err := session.EditConfig(ctx, req); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	// A candidate-datastore edit has changed nothing on the device until
	// it is committed, so reporting changed before this would be a lie
	// that a later failure could not take back.
	if target == netconf.Candidate && commit {
		if err := session.Commit(ctx); err != nil {
			return collection.Result{}, fmt.Errorf("%s: committing: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: true}, nil
}

// targetParam resolves the target datastore, refusing an unrecognized
// name here rather than letting it reach the device as an element name
// nobody defined.
func targetParam(params map[string]any) (netconf.Datastore, error) {
	switch v := strings.ToLower(strings.TrimSpace(sdk.StringParam(params, paramTarget))); v {
	case "":
		return netconf.Running, nil
	case string(netconf.Running), string(netconf.Candidate), string(netconf.Startup):
		return netconf.Datastore(v), nil
	default:
		return "", fmt.Errorf("%s: unknown datastore %q: valid values are running, candidate and startup", paramTarget, v)
	}
}

// lockParam resolves the lock policy.
func lockParam(params map[string]any) (string, error) {
	switch v := strings.ToLower(strings.TrimSpace(sdk.StringParam(params, paramLock))); v {
	case "":
		return lockNever, nil
	case lockNever, lockAlways, lockIfSupported:
		return v, nil
	default:
		return "", fmt.Errorf("%s: unknown value %q: valid values are never, always and if_supported", paramLock, v)
	}
}

// netconfSession is the narrow surface Config actually uses from a
// *netconf.Session: enough that this package's own tests can swap in a
// canned double with no real I/O, and no more. It mirrors the role
// iosSession plays in internal/catalog/net/ios.
type netconfSession interface {
	GetConfig(ctx context.Context, p datastore.Path) (datastore.Payload, error)
	EditConfig(ctx context.Context, req netconf.EditConfigRequest) error
	Lock(ctx context.Context) error
	Unlock(ctx context.Context) error
	Commit(ctx context.Context) error
	HasCapability(urn string) bool
	Close() error
}

// openSession is the seam this package's own tests swap, the same role
// internal/catalog/net/{cli,ios}'s identically-named seams play there.
// It is declared per package rather than shared for a reason the build
// enforces: a Collection package may import only pkg/
// (internal/archtest's TestCatalogPackagesImportOnlyPkg), so these
// packages cannot share an internal/ helper, and moving the seam into
// pkg/netconf would export test scaffolding into a public API.
var openSession = realOpenSession

func realOpenSession(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, target netconf.Datastore) (netconfSession, error) {
	// Checked here rather than left to sdk.ConnectPort's own identical
	// guard, because the capability refusal below names the device and
	// would dereference a nil one to do it. Found by a test asserting
	// this function does not panic, which it did.
	if device == nil {
		return nil, fmt.Errorf("%s: no target device: set the task's target or the runbook's hosts", fqcn)
	}

	// The device must implement NetconfCapable to name its port. The
	// manifest's RequiredCapabilities is what normally guarantees this,
	// so reaching the error below means something dispatched around that
	// check; it is a named refusal rather than a panic on a failed
	// assertion.
	netconfDev, ok := device.(capability.NetconfCapable)
	if !ok {
		return nil, fmt.Errorf("%s: device %q does not expose a NETCONF port (it does not implement %s)", fqcn, device.Name(), capability.NameNetconf)
	}

	conn, err := sdk.ConnectPort(ctx, rc, device, params, fqcn, netconfDev.NetconfPort())
	if err != nil {
		return nil, err
	}

	sub, err := conn.Subsystem(ctx, "netconf")
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}

	session, err := netconf.Open(ctx, sub, netconf.Options{Target: target})
	if err != nil {
		_ = sub.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}

	return &sessionAndConn{Session: session, sub: sub, conn: conn}, nil
}

// sessionAndConn embeds a *netconf.Session (promoting every operation)
// but overrides Close so one call releases the NETCONF session's own
// subsystem channel AND the underlying *remoteexec.Conn it was opened
// on, matching internal/catalog/net/{cli,ios}'s identically-named,
// independently-declared types.
//
// It closes the stream rather than sending <close-session/>, and that is
// deliberate: Close here runs from a defer that also fires on the error
// path, where the task's context may already be cancelled, and an RPC
// issued on a dead context would replace a real error with a confusing
// one. netconf.Session.Close is the polite variant, for a caller that
// has a live context and wants one.
type sessionAndConn struct {
	*netconf.Session
	sub  *remoteexec.Subsystem
	conn *remoteexec.Conn
}

// Close releases the subsystem channel and then the connection, in that
// order: the channel is layered on the connection, so closing the
// connection first would tear the channel down underneath its own
// goroutines rather than letting them be reaped.
//
// The subsystem's error is preferred when both fail, because it is the
// one closer to whatever actually went wrong.
func (s *sessionAndConn) Close() error {
	subErr := s.sub.Close()
	connErr := s.conn.Close()
	if subErr != nil {
		return subErr
	}
	return connErr
}
