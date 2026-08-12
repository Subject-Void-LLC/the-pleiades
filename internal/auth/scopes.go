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
)
