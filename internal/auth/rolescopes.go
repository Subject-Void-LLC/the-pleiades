// The Role-to-Scope mapping, and the reason it did not exist until a local
// login needed it.
//
// # Why this is new work rather than a lookup somebody forgot to write
//
// Role and Scope are two independent axes in this package, and nothing has
// ever converted between them. A JWT CARRIES both: jwt.go reads "role" and
// "scopes" straight off the claims and validates neither against anything
// stored here, so an external issuer asserts what a caller may do and this
// platform believes it. That is Federated Identity working as designed.
//
// A local login has no token to read. It has an email, a set of Teams, and
// the RoleBindings on those Teams, which ScopeResolver folds into ONE Role.
// It returns no Scope values at all, and there is no table anywhere that
// says what a viewer or an operator may actually call. Somebody has to
// decide, and chain.go's own comment already named the condition under
// which that decision becomes real: it "belongs to the phase that puts this
// rule into a running chain". Phase 79 is the first phase to derive an
// Identity from stored state rather than copy one out of a token, so the
// decision comes due here and is written down here, once, where both paths
// can see it.
//
// # The asymmetry this creates, stated rather than discovered later
//
// The JWT path ASSERTS. The local path DERIVES. A JWT can carry role=admin
// for a subject with no User row at all; a local session cannot grant more
// than this deployment's own RoleBindings say. They agree exactly when the
// external issuer agrees with the bindings, and nothing forces that.
// Reconciling the two is not this phase's work, and pretending they are the
// same mechanism would be worse than recording that they are not.
package auth

// ScopesForRole returns the scopes a locally authenticated caller holding
// role may exercise.
//
// It is the token-issuance decision for the local path, which is why it
// lives beside the vocabulary rather than in the package that checks
// passwords: scopeWildcard is unexported precisely because "granting it is
// a token-issuance decision, not something calling code should construct by
// name", so only this package can decide what a login carries. A mapping
// anywhere else would also mean the JWT path and the session path could
// eventually read two different tables.
//
// An unknown or empty role yields an empty, non-nil slice. That is the
// fail-closed answer and it is reachable in normal operation: a subject
// whose only RoleBindings are organization, group or device scoped resolves
// to no system-scope role at all, and gets a session that can reach
// nothing until the RoleBinding rule joins the admission chain. See
// chain.go for why it has not yet.
func ScopesForRole(role Role) []Scope {
	switch role {
	case RoleAdmin:
		return adminScopes()
	case RoleOperator:
		return operatorScopes()
	case RoleViewer:
		return viewerScopes()
	default:
		return []Scope{}
	}
}

// viewerScopes is every read this platform defines.
//
// A viewer sees the catalog and sees what happened. It cannot change
// anything and cannot cause anything to run.
//
// ScopeCredentialRead is included, and it is the one row here that needs an
// argument rather than an assumption. It grants the right to see that a
// credential exists, its type, and which templates bind it, and it grants
// no ability to read a secret value: the read path holds a projection with
// no field a real value could occupy, and internal/archtest forbids the API
// layer from importing the package that can decrypt. Granting it is
// granting a catalog, not a keyring, which is exactly what a viewer is for.
func viewerScopes() []Scope {
	return []Scope{
		ScopeInventoryRead,
		ScopeAnnouncementRead,
		ScopeRunbookRead,
		ScopeJobRead,
		ScopeTemplateRead,
		ScopeCredentialRead,
		ScopeScheduleRead,
		ScopeProjectRead,
	}
}

// operatorScopes is every read, plus the writes that operate the platform
// without administering it.
//
// The line drawn here is "may change the fleet and make things run" versus
// "may change who is allowed to". So an operator gets inventory writes,
// runbook execution, template and credential writes, and announcements. It
// does NOT get ScopeAccessWrite, which is the scope that lets a caller
// decide who else may do things: an operator who can grant themselves admin
// is an admin with extra steps, and that is the privilege escalation this
// whole role split exists to prevent.
func operatorScopes() []Scope {
	return append(viewerScopes(),
		ScopeInventoryWrite,
		ScopeInventoryOnboard,
		ScopeRunbookExecute,
		// Implied by ScopeRunbookExecute already; listed so a persisted
		// session records it, since that row is read as what the session
		// can do. No built-in role grants it alone: it exists for a token
		// minted with exactly the scopes a caller needs.
		ScopeRunbookCheck,
		ScopeTemplateWrite,
		ScopeCredentialWrite,
		ScopeProjectWrite,
		ScopeAnnouncementWrite,
		ScopeScheduleWrite,
	)
}

// adminScopes is the full vocabulary, ENUMERATED, never scopeWildcard.
//
// Two reasons, and the second is the one that matters.
//
// First, a wildcard would be redundant on the request path: Identity
// HasScope already short-circuits on RoleAdmin, so an admin satisfies every
// check whatever its scope list says.
//
// Second, and this is the real argument: the scope list is PERSISTED. A
// session row stores the scopes the login resolved to, and that column is
// described in its own schema as "the record of what was proven". A
// wildcard in that column is a blank cheque that outlives whatever this
// file currently means by admin: narrow the definition tomorrow and every
// live wildcard session keeps the old, wider meaning until it expires.
// Enumerating keeps the row a record of a decision rather than a deferral
// of one.
//
// The honest caveat: because of the HasScope short-circuit above, this
// enumeration is DESCRIPTIVE for an admin and load-bearing only for viewer
// and operator. It is still worth getting right, because it is what an
// operator reads when they ask what a session can do.
func adminScopes() []Scope {
	return append(operatorScopes(),
		ScopeAccessWrite,
	)
}

// AllScopes is the complete vocabulary, in a stable order.
//
// It exists so a test can assert that ScopesForRole's tables cover every
// constant this package defines. Without that assertion, adding a scope
// constant and forgetting to place it in a role would silently create a
// capability no local login can ever hold, which presents as a feature that
// simply does not work for anybody who did not log in with a JWT.
func AllScopes() []Scope {
	return []Scope{
		ScopeInventoryRead,
		ScopeInventoryWrite,
		ScopeInventoryOnboard,
		ScopeAnnouncementRead,
		ScopeAnnouncementWrite,
		ScopeRunbookRead,
		ScopeRunbookExecute,
		ScopeRunbookCheck,
		ScopeJobRead,
		ScopeTemplateRead,
		ScopeTemplateWrite,
		ScopeCredentialRead,
		ScopeCredentialWrite,
		ScopeScheduleRead,
		ScopeScheduleWrite,
		ScopeProjectRead,
		ScopeProjectWrite,
		ScopeAccessWrite,
	}
}
