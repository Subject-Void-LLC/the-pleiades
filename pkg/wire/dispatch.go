// Package wire holds the DTOs that cross a process boundary on this
// platform's own wire, rather than living duplicated inside each process
// that happens to need one.
//
// This is PLAN.md Section 25's "wire contract package" primitive: the
// entry in that section's own table reads "every DTO crossing the bus or
// the API", names the Controller, the Runner, the Python callback bridge,
// and the UI as its consumers, and assigns the general package, everything
// this doc comment is not, to Phase 15. What this phase (14) owns is
// narrower: moving DispatchPayload specifically out of its two duplicated
// definitions (internal/api/dispatcher.go and internal/runner/agent.go)
// and into one place, ahead of the general package's own construction.
// internal/api/respond.go's Link type records the identical split for the
// identical reason: Section 25's rule is that a contract has exactly one
// implementation, and a type two packages both define separately, kept
// hand-synchronized by whoever edits either one, already violates that
// rule whether or not pkg/wire itself exists yet to hold the fix. Link
// stays in internal/api a little longer only because it also needs
// auth.LinkRel, which cannot follow it here (pkg/ may not import
// internal/, enforced by internal/archtest's TestPkgNeverImportsInternal);
// DispatchPayload carries no such dependency, so nothing blocks moving it
// now instead of waiting for Phase 15.
//
// DispatchPayload's shape changed in the move, in two ways, both fixes to
// real bugs in the two duplicates it replaces rather than a cosmetic
// rename:
//
//   - The old field was named DeviceIP. Every concrete device type in this
//     codebase (internal/inventory/devices/cisco's Router and Switch,
//     internal/inventory/devices/linux's Server) exposes its management
//     address through an SSHHost() method that reads a property named
//     "host", never "ip". A field called DeviceIP was naming a property
//     that does not exist on any device this platform actually dispatches
//     to. DeviceHost names the property a publisher will really have in
//     hand.
//   - The old dispatch code (internal/api/dispatcher.go, before this
//     phase wires this package in) populated its DeviceName field from
//     device.ID(), not device.Name(): a real bug, since
//     pkg/inventory.InventoryItem declares ID() and Name() as two
//     distinct methods with two distinct meanings. DispatchPayload carries
//     both DeviceID and DeviceName as separate fields so a later publisher
//     has a place to put each value correctly, rather than one field two
//     different call sites could each be tempted to fill from whichever
//     accessor happens to compile.
//
// This package must never import anything under internal/: pkg/ is this
// project's public SDK surface, and internal/archtest's
// TestPkgNeverImportsInternal enforces that boundary in CI on every
// build.
package wire

import "github.com/Subject-Void-LLC/the-pleiades/pkg/capability"

// The keys of every flattened credential map on this platform's wire:
// DispatchPayload.Secrets below, ChildRequest.Secrets in
// collection_ipc.go, and whatever a Collection method reads back out of
// sdk.RunbookContext.InjectSecrets.
//
// They live here, in the package that declares the map itself, because
// they had grown four independent copies: internal/credential.Flatten
// writes them, internal/credtype re-declares them twice, and each
// Collection package that reads a secret re-declared them again with a
// comment explaining that it could not import the writer's copy (a
// Collection may import only pkg/). Four copies of a string that has to
// match on both sides of a process boundary is a defect waiting for
// somebody to fix a typo in three of them. There is one copy now, and
// both sides can reach it: internal/credential and internal/credtype
// both alias these rather than restating them, which is what makes the
// sentence above true rather than aspirational.
//
// A missing key means the device has no such secret. Flatten omits an
// empty field rather than writing "", so `v, ok := secrets[k]` is a real
// question with a real answer.
const (
	// SecretUsername is the account to authenticate as.
	SecretUsername = "username"

	// SecretPassword is a plaintext password.
	SecretPassword = "password"

	// SecretPrivateKeyPEM is a PEM-encoded private key.
	//
	// It is the key half of a client certificate as well as an SSH key,
	// because both are a PEM private key body and a tls.Certificate needs
	// exactly this alongside SecretCertificatePEM. Only the certificate
	// needed a new key.
	SecretPrivateKeyPEM = "private_key_pem"

	// SecretPassphrase decrypts SecretPrivateKeyPEM when that key is
	// encrypted, and unlocks SecretPFXBase64 when that is what the
	// credential carries. It is meaningless on its own.
	SecretPassphrase = "passphrase"

	// SecretCertificatePEM is a PEM-encoded X.509 client certificate, to
	// be PRESENTED rather than trusted.
	//
	// Distinct from a certificate authority bundle, which answers "whom do
	// I trust" and is transport configuration rather than a credential.
	// This answers "who am I", so it belongs with the private key that
	// proves it and travels the same path.
	//
	// Unlike every other key here the value is not itself a secret: a
	// certificate is published to whoever asks during a handshake. It
	// travels with the secrets because it is useless apart from
	// SecretPrivateKeyPEM, not because it needs hiding.
	SecretCertificatePEM = "certificate_pem"

	// SecretPFXBase64 is a PKCS#12 bundle, base64 encoded, holding a
	// certificate and its private key together.
	//
	// Base64 because this map is map[string]string and a PFX bundle is
	// binary DER, so the alternative is an encoding decided separately by
	// every reader.
	//
	// It is an ALTERNATIVE to the SecretCertificatePEM and
	// SecretPrivateKeyPEM pair, never a supplement: a credential carrying
	// both is refused rather than silently resolved in favor of one, since
	// nothing could say which the operator meant. It is unlocked by
	// SecretPassphrase at the point of use rather than anywhere earlier,
	// which is the whole point of shipping the sealed bundle.
	SecretPFXBase64 = "pfx_base64"
)

// DispatchPayload is the message body the Controller publishes to NATS
// and the Runner decodes back out, one per device, when a runbook is
// dispatched against an inventory group.
//
// It is wrapped inside an event.Event envelope on the actual wire (see
// internal/event), never published bare: the JSON tags below are what the
// envelope's own Data field decodes into on the Runner side, not the
// top-level shape of a raw NATS message.
type DispatchPayload struct {
	// JobID identifies the overall dispatch operation this payload
	// belongs to. Every device targeted by a single DispatchRunbook call
	// shares the same JobID, so the Runner and any downstream audit trail
	// can group per-device outcomes back into one logical job.
	JobID string `json:"job_id"`

	// RunbookID names what to execute against DeviceHost: a runbook id for
	// the native kind, a playbook path for the legacy one. One field
	// rather than two, because the two never coexist on one payload and a
	// second field would be empty on every dispatch.
	RunbookID string `json:"runbook_id"`

	// Mode is "check" for a dispatch that must change nothing
	// (collection.ModeCheck), and "execute" or empty for a real run. It is
	// never the only thing making a dispatch a check: a check is also
	// published to its own subject (internal/topology's CheckSubject),
	// which a Runner that predates this field never receives, because
	// such a Runner would ignore this field and run the dispatch for real.
	// A Runner treats anything from that subject as a check whatever this
	// says, and refuses a value here it does not recognize.
	Mode string `json:"mode,omitempty"`

	// ExternalChecks is whether a check may run an external Collection
	// program's Check: true only when whoever launched the job may run it
	// for real (runbook:execute), since nothing has proven a third party's
	// Check only reads. Absent or false, those tasks are reported
	// unchecked, so a Controller that does not set it fails closed. It
	// changes nothing about a real run.
	ExternalChecks bool `json:"external_checks,omitempty"`

	// PersistConnections is whether the Runner may keep one SSH connection
	// to this device open between the dispatch's tasks, rather than
	// logging in afresh for each. The Controller sets it only when both
	// the job's own setting and the device's hierarchy allow it, so off at
	// either wins. Absent or false, every task logs in afresh, which is
	// what a Controller that predates this field gets.
	PersistConnections bool `json:"persist_connections,omitempty"`

	// Kind is the launch kind this dispatch is, which is what the Runner
	// routes on to choose an execution adapter.
	//
	// An ABSENT or empty Kind means the native runbook kind, and that rule
	// lives at exactly one place (internal/adapters/routing). It is not a
	// convenience: it is what makes this field additive. A dispatch
	// published before this field existed, or by a Controller that has not
	// been upgraded yet, still routes to the adapter it was always going
	// to reach.
	//
	// The reverse direction is the one this cannot protect on its own, and
	// it is stated here so nobody has to rediscover it: every decode on
	// the Runner path is a plain json.Unmarshal with no
	// DisallowUnknownFields, so an OLD Runner receiving a NEW payload
	// silently drops this key and runs the job natively. Runners must be
	// upgraded before a template of a non-native kind is created. There is
	// no in-band mechanism that makes that safe, which is why it is a
	// deployment ordering requirement rather than a comment about one.
	//
	// No omitempty, matching Interruptible three fields below and for the
	// same reason: this struct's wire form is asserted literally in
	// pkg/wire/dispatch_test.go, and a field that sometimes vanishes makes
	// that assertion a moving target.
	Kind string `json:"kind"`

	// DeviceID is the inventory item's stable identifier
	// (pkg/inventory.InventoryItem.ID()), distinct from its
	// human-readable DeviceName. A later publisher needs both: the ID to
	// correlate this dispatch back to the exact inventory record, and the
	// name to log or display without a second inventory lookup.
	DeviceID string `json:"device_id"`

	// DeviceName is the inventory item's display name
	// (pkg/inventory.InventoryItem.Name()). It is deliberately a separate
	// field from DeviceID, not a fallback or an alias for it, because the
	// two duplicated predecessors of this type conflated them: populating
	// DeviceName from ID() was the bug this split exists to make
	// impossible to repeat.
	DeviceName string `json:"device_name"`

	// DeviceHost is the address the Runner connects to, the value every
	// concrete device type's SSHHost() method returns. It replaces the
	// old DeviceIP field, which named a property ("ip") no device type in
	// this codebase actually populates.
	DeviceHost string `json:"device_host"`

	// Interruptible carries runbook.Runbook.Interruptible's own resolved
	// value (itself engine.Metadata.IsInterruptible()'s answer) across
	// the wire, so the Runner can decide whether to self-abort this
	// execution on lost lease heartbeat without a second lookup back to
	// the Controller (PLAN.md Section 16's Network Partitions
	// mitigation). No omitempty, matching every other field on this
	// struct: an absent key on the wire would decode to Go's own bool
	// zero value (false, "not interruptible"), silently inverting the
	// safe default engine.Metadata's own nil-means-true convention
	// establishes upstream of this struct.
	Interruptible bool `json:"interruptible"`

	// SSHPort is the port a capability.SSHTransportCapable device reports
	// via SSHPort(), carried across the wire because the Runner has no
	// inventory backend of its own to re-derive it from (Phase 16, Native
	// Go Execution Adapter). Zero when the device does not declare
	// SSHTransportCapable; a zero value is never dialed, since dispatch is
	// only ever routed to a transport whose required capability the device
	// actually has.
	SSHPort int `json:"ssh_port"`

	// Capabilities is the device's own Capabilities() result at the moment
	// the Controller admitted it for this job (internal/dispatch's
	// CapabilityAdmits already ran the real structural check against the
	// real inventory item before this payload was built). The Runner
	// trusts this list as a membership check rather than re-deriving it,
	// which is sound only because that structural check already happened
	// upstream of this payload ever existing.
	Capabilities []capability.Name `json:"capabilities"`

	// DeviceType and DeviceProperties let the Runner rebuild the device as
	// its real type (record.LookupType), so every accessor answers on the
	// Runner as it does on the Controller. DeviceProperties holds only the
	// keys that type declares its accessors read
	// (record.RegisterDispatchProperties) and the device's discovery,
	// never any other property: an operator may keep anything in one. Both
	// are absent from an older Controller's payload, and a Runner given
	// none falls back to a device built from DeviceHost and SSHPort alone.
	DeviceType       string         `json:"device_type,omitempty"`
	DeviceProperties map[string]any `json:"device_properties,omitempty"`

	// Secrets is the flattened credential for DeviceName, resolved by the
	// Controller at dispatch time (PLAN.md Section 17's Just-in-Time
	// delivery principle: attached directly to the payload, never
	// pre-distributed to the Runner). Empty when the device has no stored
	// credential, which is not itself a dispatch failure: only a task that
	// actually needs a secret fails downstream, the same place a missing
	// credential already fails at the Crawl tier.
	//
	// The keys are the Secret* constants declared at the top of this file,
	// which is the authority rather than any package that aliases them. That
	// matters because the set has grown: it was the four SSH keys until
	// Phase 78d added SecretCertificatePEM and SecretPFXBase64, and a doc
	// comment here enumerating a closed set is a doc comment that goes
	// quietly out of date in the one file the whole vocabulary is defined
	// in. omitempty keeps a credential-less dispatch's wire form free of a
	// bare "secrets":{}.
	Secrets map[string]string `json:"secrets,omitempty"`

	// Tags is the device's own pkg/inventory.InventoryItem.Tags() result at
	// dispatch time, carried as plain strings rather than
	// []inventory.Tag: this package must never import internal/, and
	// pkg/inventory.Tag would add an unwanted cross-package coupling this
	// wire type has otherwise deliberately avoided (see this file's own
	// doc comment on DeviceID/DeviceName). Added for
	// internal/adapters/legacy (Phase 17, Legacy Ansible Adapter), whose
	// generated inventory.json needs a device's group membership and had
	// no field to read it from: Tag already maps 1:1 onto an Ansible
	// inventory group in this codebase's own worked example
	// (examples/upgrade_ios/pleiades/inventory.yaml's "tags: [catalyst_lab]"
	// pairs with examples/upgrade_ios/ansible/inventory.ini's
	// "[catalyst_lab]" group header). omitempty keeps an untagged
	// dispatch's wire form free of a bare "tags":[].
	Tags []string `json:"tags,omitempty"`

	// Fields is the resolved launch.Resolved.Fields (AWX_PARITY_ROADMAP.md
	// Section 3b.1) this dispatch was launched with: forks, limit,
	// verbosity, timeout, and whichever kind-specific fields the launch's
	// kind declares (e.g. job_tags/skip_tags for the playbook kind). It is
	// carried as plain map[string]any rather than internal/launch.Fields:
	// this package must never import internal/ (see this file's own doc
	// comment on why DeviceID/DeviceName stay plain too), and the two
	// types share an identical underlying type, so
	// internal/adapters/legacy and internal/adapters/native convert back
	// with a bare type conversion (launch.Fields(payload.Fields)) to reuse
	// launch.Fields' own typed accessors rather than re-deriving them here.
	//
	// This is the second of the two wire hops the roadmap's own Section
	// 3b.1 names: internal/dispatch.Job already captured this value on the
	// job record (the first hop); this field is what finally lets it reach
	// a Runner. omitempty keeps a dispatch launched with no fields set free
	// of a bare "fields":{}, matching Secrets and Tags above; an absent key
	// decodes to a nil map, which every reader here treats identically to
	// an empty one.
	Fields map[string]any `json:"fields,omitempty"`

	// ExtraVars is the resolved launch.Resolved.ExtraVars this dispatch was
	// launched with: the template's defaults, a saved configuration, and
	// this launch's own overrides, already merged in that precedence
	// order. Same additive, omitempty, plain-map-not-launch.Fields
	// reasoning as Fields above, and it is genuinely a separate field
	// rather than Fields["extra_vars"]: launch.Resolved itself already
	// pulls it out for the identical convenience (internal/launch's own
	// resolve.go), and duplicating that split here means a Runner never
	// has to know "extra_vars" is the one Fields key that means something
	// different from the rest.
	ExtraVars map[string]any `json:"extra_vars,omitempty"`

	// Injected is what this dispatch's bound credentials produce:
	// environment variables, extra variables, generated files and vault
	// identities, all already rendered by the Controller at fan-out.
	//
	// A pointer with omitempty, unlike the maps above, because "no
	// credentials were bound" and "credentials were bound and produced
	// nothing" are different facts a Runner may eventually need to
	// distinguish, and because this struct's literal wire form is asserted
	// in dispatch_test.go: a nil pointer vanishes cleanly where an empty
	// struct would add "injected":{} to every credential-less dispatch.
	//
	// It carries the same deployment-ordering warning Kind above carries,
	// and for the identical mechanical reason: every Runner-side decode is
	// a plain json.Unmarshal with no DisallowUnknownFields, so an OLD
	// Runner receiving a NEW payload silently drops this key and runs the
	// job with nothing injected. The run then fails wherever the playbook
	// first needed the secret, which is nowhere near the cause. **Runners
	// must be upgraded before a credential is bound to a template.** There
	// is no in-band mechanism that makes that safe, which is why it is a
	// deployment ordering requirement rather than a comment about one.
	Injected *Injected `json:"injected,omitempty"`
}

// Injected is the rendered output of a dispatch's bound credentials.
//
// It is the wire form of internal/credtype.Artifact, which cannot cross
// this boundary itself: this package must never import internal/. The two
// are converted in exactly one place (internal/dispatch), so a field added
// to one and not the other is a compile failure at that conversion rather
// than a value silently dropped on the wire.
//
// What it deliberately does NOT carry is the machine credential. That
// already has a home in DispatchPayload.Secrets, which every adapter and
// every Collection method already reads, and a second home would mean two
// answers to "what does this run authenticate as."
type Injected struct {
	// Env are environment variables to set for the run.
	//
	// PLAN.md Section 17.5 forbids secrets in a process environment, and
	// Section 29.4 resolves the one place that rule cannot hold: an Ansible
	// module reads the environment by design, and the ephemeral container
	// the legacy adapter runs a playbook in is the trust boundary that
	// makes it acceptable. The native Go mesh keeps the stricter rule and
	// REFUSES this field rather than honouring it; see
	// internal/adapters/native for the refusal and for why it is a refusal
	// rather than a silent skip.
	Env map[string]string `json:"env,omitempty"`

	// ExtraVars are extra variables to merge into the run, including
	// nested structures.
	ExtraVars map[string]any `json:"extra_vars,omitempty"`

	// Files are generated credential files, sorted by path.
	Files []InjectedFile `json:"files,omitempty"`

	// Vault are the Ansible Vault identities the run must be given, each
	// naming a file in Files.
	Vault []InjectedVault `json:"vault,omitempty"`

	// Mask are the values above that must be scrubbed from any output this
	// run produces, longest first.
	//
	// It exists because SECRECY IS NOT RECOVERABLE FROM THE OTHER FIELDS.
	// An adapter holds an environment map and a file body; a token and a
	// region look identical by the time they arrive. The Controller knows
	// which values came from inputs marked secret, and this is where it
	// says so.
	//
	// Getting it wrong in the permissive direction is worse than it sounds,
	// which is why this field exists rather than an adapter simply masking
	// everything it was handed. Registering every injected value would
	// scrub an ordinary URL, region or username out of every later line for
	// the rest of the process, corrupting output without protecting
	// anything.
	//
	// Carrying the values themselves adds no exposure this payload does not
	// already have: they are the same bytes Env, ExtraVars and Files
	// already carry, and the payload's JetStream retention is the residual
	// recorded against the whole dispatch rather than against this field.
	Mask []string `json:"mask,omitempty"`
}

// InjectedFile is one generated credential file.
type InjectedFile struct {
	// Label is the multi-file label this file was generated under, and
	// empty for the single-file spelling.
	Label string `json:"label,omitempty"`

	// Path is where the file is written wherever the job runs.
	Path string `json:"path"`

	// Content is the rendered file body.
	Content string `json:"content"`

	// Mode is the permission bits, always 0o600.
	Mode int64 `json:"mode"`
}

// InjectedVault is one Ansible Vault identity.
//
// It is carried separately from the file holding its password because the
// file alone is not enough: ansible-playbook has to be told about it with
// --vault-id <identifier>@<path>, and the identifier is what lets a
// playbook encrypted under two vault identities be decrypted in one run.
type InjectedVault struct {
	// Identifier is the vault label, and empty for Ansible's own default
	// vault identity.
	Identifier string `json:"identifier,omitempty"`

	// Path is the password file this identity reads, which is one of the
	// paths in Injected.Files.
	Path string `json:"path"`
}
