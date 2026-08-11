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
	// ScopeInventoryRead grants read access to inventory items and groups.
	ScopeInventoryRead Scope = "inventory:read"
	// ScopeInventoryWrite grants create/update/delete access to inventory
	// items and groups.
	ScopeInventoryWrite Scope = "inventory:write"
	// ScopeRunbookExecute grants the right to dispatch a Runbook against a
	// target. This is api.Dispatcher's own required scope.
	ScopeRunbookExecute Scope = "runbook:execute"
	// ScopeJobRead grants the right to read a job's status and stream its
	// logs. This is api.LogStreamer's own required scope; unlike the
	// other three, no phase before this one ever checked it.
	ScopeJobRead Scope = "job:read"
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
