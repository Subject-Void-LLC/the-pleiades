package auth

// Scope names one unit of authorization a token can carry and an
// AdmissionRule can require. It replaces a bare string: PLAN.md Section
// 18.4 and this package's own Identity.Scopes field already treated scope
// names as a fixed, small vocabulary ("inventory:read", "runbook:execute")
// rather than free-form text, but nothing enforced that until now, so a
// typo in a literal (e.g. "runbook:excute") silently compiled as "grant a
// scope nobody will ever hold" instead of failing at build time.
//
// AGENTS.md's typing rule ("Define custom types for domain concepts")
// applies here for the same reason it already applies to DeviceID: a
// scope is not an arbitrary string, it is a member of a closed set this
// file defines.
type Scope string

// scopeWildcard is the one Scope value Identity.HasScope treats
// specially: an identity holding it satisfies every RequiredScope,
// regardless of what this file adds to the vocabulary below. Kept
// unexported since granting it is a token-issuance decision, not
// something calling code should construct by name.
const scopeWildcard Scope = "*"

// The scope vocabulary this platform's admission chain checks tokens
// against today. Each constant is the exact wire value a JWT's "scopes"
// claim carries (PLAN.md Section 18.5's Personal Access Token scoping),
// so adding a new checked capability means adding a constant here first,
// per AGENTS.md's Architecture Mismatch protocol ("a map that lags the
// code is useless").
const (
	// ScopeInventoryRead grants read access to inventory items, groups,
	// and the Inventories that contain them.
	//
	// One scope covers all three deliberately. A scope names what kind of
	// operation a token may perform; *which* inventory it may perform it
	// on is the RBAC target's question, answered by ScopeInventory
	// bindings and the hierarchical resolver. Splitting the scope per
	// container would put the same decision in two places and guarantee
	// they eventually disagree.
	ScopeInventoryRead Scope = "inventory:read"
	// ScopeInventoryWrite grants create/update/delete access to inventory
	// items, groups, and Inventories.
	ScopeInventoryWrite Scope = "inventory:write"
	// ScopeAnnouncementRead grants the right to see operator
	// announcements. It is separate from every other read scope because
	// its natural audience is the widest one this platform has: anybody
	// who can sign in should see a change freeze, including identities
	// that may read nothing else.
	ScopeAnnouncementRead Scope = "announcement:read"
	// ScopeAnnouncementWrite grants the right to post, edit and retire
	// announcements.
	//
	// Deliberately its own scope rather than folded into an
	// administrative catch-all. An announcement is an instruction to
	// every operator about how to run production, delivered with the
	// platform's own authority, so who may write one is worth being able
	// to grant and audit on its own.
	ScopeAnnouncementWrite Scope = "announcement:write"
	// ScopeRunbookExecute grants the right to dispatch a Runbook against a
	// target. This is api.Dispatcher's own required scope.
	ScopeRunbookExecute Scope = "runbook:execute"
	// ScopeJobRead grants the right to read a job's status and stream its
	// logs. This is api.LogStreamer's own required scope; unlike the
	// other three, no phase before this one ever checked it.
	ScopeJobRead Scope = "job:read"
	// ScopeAccessRead and ScopeAccessWrite cover Organizations, Teams, Users
	// and RoleBindings together.
	//
	// Two scopes rather than eight, following the rule ScopeInventoryRead
	// states above: a scope names what kind of operation a token may
	// perform, and which object it may perform it on is the RBAC target's
	// question. Eight would put one decision in two places, and the two
	// places would drift.
	//
	// Write is worth granting and auditing on its own even more than
	// announcement:write is. Everything else in this vocabulary lets a
	// caller act on the fleet; this one lets a caller decide who else may,
	// which makes it the only scope that can be used to grant itself.
	ScopeAccessRead  Scope = "access:read"
	ScopeAccessWrite Scope = "access:write"

	// ScopeSettingsRead and ScopeSettingsWrite cover the deployment's own
	// configuration: how people authenticate, what every run inherits, how
	// long records are kept, where logs go.
	//
	// Its own pair rather than folded into access:*, and the split is worth
	// stating. access:write decides who may reach what inside this
	// deployment; settings:write decides what this deployment IS -- which
	// directory authenticates it, which callback URLs it hands to an
	// identity provider, how long an audit record survives before it is
	// purged. Somebody who administers role bindings does not thereby
	// acquire the ability to point authentication at a directory they
	// control, and that is exactly the escalation folding them together
	// would allow.
	//
	// Read is separate from write for the same reason it is everywhere else
	// here, and carries more weight on this surface than most: a settings
	// page names an LDAP bind account, an aggregator endpoint and a base
	// URL, which is a map of the estate even with every secret redacted.
	ScopeSettingsRead  Scope = "settings:read"
	ScopeSettingsWrite Scope = "settings:write"

	// ScopeTemplateRead and ScopeTemplateWrite cover Templates: the saved,
	// reusable definitions of what this platform runs, where, and how.
	//
	// Not folded into the runbook scopes above, and the split is the
	// security-relevant half of this pair. A runbook is a file; a template
	// is a saved instruction to run something against a named set of hosts,
	// with a declared set of fields a launching operator may and may not
	// change. Someone who may edit a template can change what a launch
	// actually does without holding execute at all, and can widen which
	// fields a launcher may set, so authoring templates is a privilege
	// worth granting and revoking on its own rather than one acquired as a
	// side effect of being allowed to browse the catalog.
	//
	// Launching is deliberately NOT ScopeTemplateWrite: running a saved
	// definition is ScopeRunbookExecute, the same scope every other
	// dispatch path checks, so an operator who may run things does not
	// thereby acquire the right to change what they run.
	ScopeTemplateRead  Scope = "template:read"
	ScopeTemplateWrite Scope = "template:write"

	// ScopeRunbookRead grants the right to browse the runbook catalog and
	// read one runbook's compiled capability requirements.
	//
	// It is separate from ScopeRunbookExecute rather than folded into it
	// because browsing what a platform can do and being allowed to do it
	// are different grants: an operator reviewing which runbooks exist,
	// or a UI rendering a catalog, needs the first and must not thereby
	// acquire the second. Granting execute does not imply read here
	// either -- the admission chain checks each scope by name, so a token
	// meant to browse and launch carries both.
	ScopeRunbookRead Scope = "runbook:read"

	// ScopeCredentialRead grants the right to see that a credential
	// exists, what type it is, and which templates bind it.
	//
	// It never grants the right to read a secret value, and that is a
	// property of the code rather than of this comment: no endpoint
	// returns one. The read path holds a credstore.Store, whose projection
	// replaces every secret input with a redaction marker and has no field
	// a real value could occupy, and the interface that can decrypt lives
	// in a package internal/api is forbidden by internal/archtest from
	// importing. Granting this scope to somebody is granting them a
	// catalog, not a keyring.
	ScopeCredentialRead Scope = "credential:read"

	// ScopeCredentialWrite grants create, update and delete of credentials
	// and custom credential types, and binding a credential to a template.
	//
	// Binding is deliberately here rather than under ScopeTemplateWrite,
	// and the distinction is the same one ScopeTemplateWrite and
	// ScopeRunbookExecute already draw from the other direction. A template
	// author decides WHAT runs. Whoever binds a credential decides what it
	// runs AS, which is the higher privilege of the two: it is the
	// difference between writing a playbook and choosing which production
	// account executes it. An operator trusted to maintain templates does
	// not thereby acquire the right to point one at the domain admin
	// credential.
	ScopeCredentialWrite Scope = "credential:write"

	// ScopeScheduleRead grants the right to see schedules, their upcoming
	// occurrences, and the history of what they have and have not run.
	ScopeScheduleRead Scope = "schedule:read"

	// ScopeScheduleWrite grants create, update, enable, disable and delete
	// of schedules.
	//
	// It is deliberately NOT folded into ScopeRunbookExecute, even though
	// the visible effect of a schedule is that things run. The two are
	// different privileges: execute lets somebody run a template once, now,
	// under their own name and their own judgement, whereas this lets
	// somebody arrange for it to run repeatedly, unattended, after they
	// have stopped watching. The second is the larger grant, and the same
	// reasoning ScopeCredentialWrite already applies to binding applies
	// here -- deciding that something runs forever is a bigger decision
	// than deciding it runs once.
	//
	// Nor is it folded into ScopeTemplateWrite. Authoring what a template
	// does and deciding when it fires are separately useful: a release
	// engineer may own the definitions while an operations team owns the
	// calendar.
	ScopeScheduleWrite Scope = "schedule:write"
)
