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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// Parameter names come from ansible.netcommon.netconf_config rather than
// being coined here, matching this project's standing rule that a
// runbook feature reuses Ansible's vocabulary instead of inventing a new
// one. content, default_operation, error_option, lock, commit and backup
// are all that module's own names, with the same meanings. datastore is
// that module's own alias for its target parameter: target itself is the
// engine's device selector (collection.TargetParam), which no method may
// declare, so the alias is the name used here.
const (
	paramContent          = "content"
	paramDatastore        = "datastore"
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
	doc := configDoc()
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
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			NoBecomeReason:  "it speaks NETCONF over an SSH subsystem, which no sudo prefix reaches, so it would run as the login",
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "An edit-config merge has no reliable inverse: deleting what was added is not the same as restoring what was replaced, and this method cannot know which of the two a given element did. Set backup: true to capture the prior configuration for a human-directed rollback. Note that rollback-on-error, which this method requests whenever the device supports it, covers a DIFFERENT case: it undoes a partially applied edit that the device itself rejected, not one that succeeded and was later regretted.",
			},
			NoCheckReason: "only applying the document says what the device makes of it: the running datastore has no staging " +
				"area, and a check through the candidate datastore would stage the document on the device, which a " +
				"check promises not to do",
			Doc:   doc,
			Facts: collection.DeclareFacts(doc.Returns, discoveryFactKeys, factContentID),
		},
		Invoke: Config,
	})
}

func configDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Applies a configuration document to a device over NETCONF, with an optional pre-change backup.",
		Description: "Opens an RFC 6241 NETCONF session over the SSH \"netconf\" subsystem and applies content to the chosen datastore with edit-config. Parameter names are ansible.netcommon.netconf_config's own, with its target parameter under its own alias, datastore, since target names the device a task runs on. The session negotiates RFC 6242 chunked framing whenever the device offers base:1.1, and requests rollback-on-error whenever the device advertises it, so a rejected document leaves the device unchanged rather than half configured; that matters most on a device offering only writable-running, which is what Cisco IOS XE offers, because such a device has no staging area and every element lands on the live configuration as it is applied. A datastore the device never advertised support for is refused when the session opens rather than at the first write, naming the missing capability. Unlike the net.cli.* and net.ios.config methods, a rejected element comes back as a structured error carrying the device's own error-tag and the XPath of the element it objected to. Reports changed whenever the document reaches the device and the device answers ok.",
		Params: []collection.Param{
			{Name: paramContent, Type: "string", Required: true, Description: "The configuration document to apply, as the XML that goes inside edit-config's <config> element. Per-element operations are expressed the standard way, with an nc:operation attribute; pair that with default_operation: none so the device changes only what the document explicitly names."},
			{Name: paramDatastore, Type: "string", Choices: []string{"running", "candidate", "startup"}, ChoicesIgnoreCase: true, Description: "The datastore to configure: running, candidate or startup. Left unset it is chosen when the session opens, from what the device's hello advertises that session: running when it offers :writable-running, otherwise candidate (locked, validated when the device offers :validate, then committed). That is chosen afresh on every task, since a device's offer changes with its configuration: Cisco IOS XE offers only running until netconf-yang feature candidate-datastore is configured, and then only candidate. A datastore the device does not advertise is refused before anything is applied."},
			{Name: paramDefaultOperation, Type: "string", Default: "merge", Choices: []string{"merge", "replace", "none"}, Description: "What the device does with elements carrying no explicit operation attribute: merge, replace or none. RFC 6241 defines no \"delete\" here; express a delete with an nc:operation attribute in content."},
			{Name: paramErrorOption, Type: "string", Choices: []string{"stop-on-error", "continue-on-error", "rollback-on-error"}, Description: "How the device handles a rejected element: stop-on-error, continue-on-error or rollback-on-error. Left unset this method asks for rollback-on-error whenever the device advertises the capability, and otherwise leaves the device its own default, stop-on-error, because stop-on-error leaves a rejected document half applied."},
			{Name: paramLock, Type: "string", Default: "never", Choices: []string{"never", "always", "if_supported"}, ChoicesIgnoreCase: true, Description: "Whether to lock the datastore for the duration: never, always, or if_supported. Locking prevents another client changing the datastore mid-edit; it also blocks every other client, which matters on a shared device. The candidate datastore is always locked, and never is refused there: the lock is refused while another client's changes sit in the candidate, so this task commits and discards only its own edit."},
			{Name: paramCommit, Type: "bool", Default: "true", Description: "On the candidate datastore, commit after the edit, which is validated first when the device offers :validate. false validates the edit and discards it, so it is checked by the device and applied nowhere, and reports no change, with validated and applied saying what ran; unlike ansible.netcommon.netconf_config it never leaves the edit staged, where the next commit by any client would apply it. false is refused when the session writes running, where an edit is live at once."},
			{Name: paramBackup, Type: "bool", Default: "false", Description: "Capture the running datastore's full contents with get-config before applying anything, whichever datastore the task writes, recorded under the backup stat with every secret masked. A configuration masking cannot vouch for fails the task before anything is applied."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: append([]collection.ReturnField{
			{Name: statValidated, Type: "bool", Returned: "when commit is false", Description: "Whether the device validated the candidate edit before it was discarded: false on a device that does not offer :validate, where only the edit itself was accepted."},
			{Name: statApplied, Type: "bool", Returned: "when commit is false", Description: "Always false: the edit was discarded, not applied."},
			{Name: statBackup, Type: "string", Returned: "when backup is true", Description: "The datastore's full contents as XML, captured immediately before this task's own document was applied, with every secret masked: the text of each leaf whose name holds a secret (password, secret, a key such as auth-key or pre-shared-key, encrypted-password, RFC 8177's key-string, keystring and hexadecimal-string, OpenConfig's key-clear-text, an SNMP community), of every other leaf inside such an element at any depth but its encryption type, id, algorithm or lifetime (and, two or more levels down outside SNMP, a name or address), and of each crypt-shaped value ($1$, $6$, $9$) is replaced by pleiades-masked- and sixteen hexadecimal digits, equal secrets alike within one run; the run's own credential is masked wherever it appears, other leaves are read by the configuration text masker net.ios.config's backup uses, and comments are withheld. Every element, name, address and encryption type stays where it was. A document that is not well-formed XML, holds a DOCTYPE, nests past 256 elements, has an attribute that may hold a secret, or has a line the text masker cannot vouch for fails the task before anything is applied, and none of it is recorded. net.netconf.config refuses content holding a placeholder, so this text cannot be applied back as it is."},
		}, discoveryReturns()...),
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
	// Content built from a masked read would set each secret it carries to
	// its placeholder; refused before the session opens.
	if err := netcfg.RefuseMasked(paramContent, content); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	target, err := datastoreParam(params)
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

	discovered := session.Discovery()
	warnings, err := recordDiscovery(rc, params, discovered)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	result, err := apply(ctx, rc, session, discovered, configRequest{
		content: content, lock: lock, lockSet: hasParam(params, paramLock), backup: backup, commit: commit,
		defaultOperation: sdk.StringParam(params, paramDefaultOperation), errorOption: sdk.StringParam(params, paramErrorOption),
	})
	for _, w := range session.TakeWarnings() {
		warnings = append(warnings, "the device warned: "+w.Error())
	}
	if len(warnings) > 0 {
		if werr := sdk.RecordWarnings(rc, warnings); werr != nil && err == nil {
			err = werr
		}
	}
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return result, nil
}

// configRequest is what a task asked of one session.
type configRequest struct {
	content          string
	lock             string
	lockSet          bool
	backup           bool
	commit           bool
	defaultOperation string
	errorOption      string
}

// statValidated and statApplied say what a candidate edit that was not
// committed did: whether the device validated it, and that it was not
// applied.
const (
	statValidated = "validated"
	statApplied   = "applied"
)

// apply runs req on an open session, choosing nothing the session's
// discovery did not allow.
func apply(ctx context.Context, rc sdk.RunbookContext, session netconfSession, discovered netconf.Discovery, req configRequest) (collection.Result, error) {
	target := session.Target()
	candidate := target == netconf.Candidate
	if !req.commit && !candidate {
		return collection.Result{}, fmt.Errorf("%s: false validates an edit in the candidate datastore and discards it, but this session writes %s, where an edit is live at once; set %s: candidate on a device that advertises it", paramCommit, target, paramDatastore)
	}
	if candidate && req.lockSet && req.lock == lockNever {
		return collection.Result{}, fmt.Errorf("%s: never is refused on the candidate datastore: without the lock another client's staged changes would be committed with this edit, and a failed edit could not be discarded safely", paramLock)
	}
	unserved, err := unservedNamespaces(req.content, discovered)
	if err != nil {
		return collection.Result{}, err
	}
	if len(unserved) > 0 {
		return collection.Result{}, fmt.Errorf("%s names %s, which no YANG module of the %d the device implements has as its namespace; nothing was sent", paramContent, strings.Join(unserved, ", "), len(discovered.Modules))
	}

	// The candidate is always locked, so this task commits only its own
	// edit and discards only what it staged itself: a lock is refused while
	// another client's changes sit in the candidate.
	locked := false
	if candidate || req.lock == lockAlways || (req.lock == lockIfSupported && session.HasCapability(netconf.CapabilityCandidate)) {
		if err := session.Lock(ctx); err != nil {
			if candidate || req.lock == lockAlways {
				return collection.Result{}, fmt.Errorf("locking the %s datastore: %w", target, err)
			}
		} else {
			locked = true
			defer func() { _ = session.Unlock(ctx) }()
		}
	}
	discard := func(cause error) error {
		if candidate && locked {
			if err := session.DiscardChanges(ctx); err != nil {
				return fmt.Errorf("%w; discarding the staged edit failed too: %v", cause, err)
			}
		}
		return cause
	}

	if req.backup {
		// The configuration in effect is running's, whichever datastore
		// this task writes.
		payload, err := session.GetConfigFrom(ctx, netconf.Running, datastore.Path{})
		if err != nil {
			return collection.Result{}, fmt.Errorf("capturing backup: %w", err)
		}
		// Recorded masked, never as the device has it, and before anything
		// is applied: a backup masking cannot vouch for stops the task here,
		// so the change never goes ahead without the backup it asked for.
		masked, err := maskedBackup(rc, string(payload.Bytes))
		if err != nil {
			return collection.Result{}, fmt.Errorf("capturing backup: %w", err)
		}
		if err := rc.SetStat(statBackup, masked); err != nil {
			return collection.Result{}, err
		}
	}

	if err := session.EditConfig(ctx, netconf.EditConfigRequest{Config: req.content, DefaultOperation: req.defaultOperation, ErrorOption: req.errorOption}); err != nil {
		return collection.Result{}, discard(err)
	}
	if !candidate {
		return collection.Result{Changed: true}, nil
	}
	// A candidate edit has changed nothing on the device until it is
	// committed, so reporting changed before this would be a lie a later
	// failure could not take back.
	validated := false
	if session.CanValidate() {
		if err := session.Validate(ctx); err != nil {
			return collection.Result{}, discard(fmt.Errorf("validating the candidate: %w", err))
		}
		validated = true
	}
	if !req.commit {
		if err := session.DiscardChanges(ctx); err != nil {
			return collection.Result{}, fmt.Errorf("discarding the validated edit: %w", err)
		}
		if err := rc.SetStat(statValidated, validated); err != nil {
			return collection.Result{}, err
		}
		return collection.Result{Changed: false}, rc.SetStat(statApplied, false)
	}
	if err := session.Commit(ctx); err != nil {
		return collection.Result{}, discard(fmt.Errorf("committing: %w", err))
	}
	return collection.Result{Changed: true}, nil
}

// hasParam reports whether the task set name at all.
func hasParam(params map[string]any, name string) bool {
	_, ok := params[name]
	return ok
}

// maskedBackup is a datastore's XML masked for the backup stat through
// netcfg.MaskXML: each secret leaf is replaced by its placeholder, the run's
// own credential is masked wherever it appears whole, and each secret found
// that is long enough joins the run's masking set, so a later task printing
// it is masked too. A document masking cannot vouch for returns an error and
// none of the text.
func maskedBackup(rc sdk.RunbookContext, doc string) (string, error) {
	masked, found, err := netcfg.MaskXML(doc, credentialValues(rc.InjectSecrets())...)
	if err != nil {
		return "", fmt.Errorf("the configuration is not recorded, since masking cannot vouch for it: %w", err)
	}
	sdk.RegisterSecret(rc, netcfg.Learnable(found)...)
	return masked, nil
}

// credentialValues are the secret values of the credential a run handed this
// method, leaving out the fields the engine itself does not mask by value
// (a username, a certificate, a seed's login and host), the same set the
// engine's credentialSecrets keeps.
func credentialValues(secrets map[string]string) []string {
	var out []string
	for key, value := range secrets {
		switch key {
		case wire.SecretUsername, wire.SecretCertificatePEM, wire.SecretSeedUsername, wire.SecretSeedAuthorizedKey, wire.SecretSeedHost:
			continue
		}
		out = append(out, value)
	}
	return out
}

// datastoreParam resolves the datastore to configure, refusing an
// unrecognized name here rather than letting it reach the device as an
// element name nobody defined.
func datastoreParam(params map[string]any) (netconf.Datastore, error) {
	switch v := strings.ToLower(strings.TrimSpace(sdk.StringParam(params, paramDatastore))); v {
	case "":
		// Chosen when the session opens, from what the device advertises.
		return "", nil
	case string(netconf.Running), string(netconf.Candidate), string(netconf.Startup):
		return netconf.Datastore(v), nil
	default:
		return "", fmt.Errorf("%s: unknown datastore %q: valid values are running, candidate and startup", paramDatastore, v)
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
	GetConfigFrom(ctx context.Context, source netconf.Datastore, p datastore.Path) (datastore.Payload, error)
	EditConfig(ctx context.Context, req netconf.EditConfigRequest) error
	Lock(ctx context.Context) error
	Unlock(ctx context.Context) error
	Commit(ctx context.Context) error
	DiscardChanges(ctx context.Context) error
	Validate(ctx context.Context) error
	CanValidate() bool
	HasCapability(urn string) bool
	Target() netconf.Datastore
	Discovery() netconf.Discovery
	TakeWarnings() []netconf.RPCError
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

	// The YANG library is cached for the run by the device's identity, and
	// only for a session whose host key was verified.
	cacheKey := string(device.ID())
	if skip, _ := sdk.BoolParamOr(params, sdk.ParamInsecureSkipHostKeyVerify, false); skip {
		cacheKey = ""
	}
	session, err := netconf.Open(ctx, sub, netconf.Options{Target: target, ChooseTarget: target == "", DiscoveryCacheKey: cacheKey})
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
