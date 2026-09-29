// Package auth's hypermedia half: the port that answers which affordances
// an authenticated caller may actually exercise, and the admission-backed
// implementation every composition root wires.
//
// This file replaces a port that asked for HTTP verbs against a raw
// endpoint string. That shape could not name a link relation (so Phase
// 13's own Release Gate, "the _links array actively omits the delete URL,"
// was literally inexpressible), carried no Identity to decide anything
// with, and took a caller-supplied path rather than a matched route.
// It had exactly one implementation anywhere in this repository, a test
// mock, so production could pass only nil.
package auth

import (
	"context"
	"fmt"
)

// LinkRel is the relation name of one hypermedia affordance: the "rel" of
// RFC 8288 and of the _links array this platform emits.
//
// It is a named type rather than a bare string for the same reason Scope
// is (scopes.go): a relation name is a member of a closed vocabulary this
// file defines, and a typo in a literal ("delte") must fail at build time
// rather than silently name an affordance no client will ever match.
type LinkRel string

// The affordance vocabulary a route table may declare today. Adding a new
// relation means adding a constant here first, per AGENTS.md's
// Architecture Mismatch protocol: a map that lags the code is useless.
const (
	// RelSelf is the canonical link to the resource that produced this
	// response. RFC 8288 reserves the name for exactly this.
	RelSelf LinkRel = "self"

	// RelDelete is the affordance that retires the resource. Phase 13's
	// Release Gate names this one specifically: a read-only caller must
	// not see it.
	RelDelete LinkRel = "delete"

	// RelUpdate is the affordance that modifies the resource in place.
	RelUpdate LinkRel = "update"

	// RelCreate is the affordance that creates a new member of a
	// collection.
	RelCreate LinkRel = "create"

	// RelCollection is the affordance that lists the collection a
	// resource belongs to. IANA registers the name for exactly this
	// (RFC 6573), and it is separate from RelSelf on purpose: a listing
	// route and a member route are two different affordances, so folding
	// both onto "self" would make a permitted result ambiguous about
	// which of the two it granted.
	RelCollection LinkRel = "collection"

	// RelExecute is the affordance that dispatches work against the
	// resource. api.Dispatcher's own relation.
	RelExecute LinkRel = "execute"

	// RelCopy is the affordance that duplicates the resource.
	//
	// Its own relation rather than RelCreate, and not merely for tidiness.
	// A relation is unique per resource, which is what lets a caller
	// correlate a permitted result back to a method and an href, so a copy
	// sharing "create" with the collection's own create would give a
	// template's page two affordances a client cannot tell apart and two
	// buttons the UI cannot label separately. They are also different
	// questions: one makes a new record from a form, the other makes one
	// from an existing record.
	RelCopy LinkRel = "copy"

	// RelLogs is the affordance that streams a job's live output.
	// api.LogStreamer's own relation.
	RelLogs LinkRel = "logs"

	// RelCredentials is the affordance that replaces what a template runs
	// as.
	//
	// Its own relation rather than RelUpdate, for the reason RelCopy gives
	// above and with a sharper consequence here. A relation is unique per
	// resource, so sharing "update" with the template's own edit would
	// give one page two affordances a client cannot tell apart. It would
	// also conflate two different privileges: editing a template is
	// template:write and decides WHAT runs, while binding a credential is
	// credential:write and decides what it runs AS, which is the higher
	// of the two. A client reading "update" and inferring it may do both
	// would be inferring wrongly.
	RelCredentials LinkRel = "credentials"

	// RelSetInputs is the affordance that replaces a credential type's
	// input schema.
	//
	// Its own relation rather than RelUpdate, for the reason RelCredentials
	// gives: a relation is unique per resource, and a credential type
	// already uses "update" for its metadata edit. A second affordance
	// sharing that relation would give one resource two candidates a client
	// cannot tell apart, and two controls the UI cannot label separately.
	// It carries the same credential:write scope the metadata edit does; the
	// separation is about identifying the affordance, not about a different
	// privilege.
	RelSetInputs LinkRel = "set-inputs"

	// RelCancel is the affordance that stops work already in flight.
	//
	// Its own relation rather than RelExecute or RelDelete, and the contrast
	// with each is the reason. Execute STARTS the work this cancels, so a
	// resource offering both under one relation would give a client no way
	// to tell "run it" from "stop it", which are opposites. Delete retires
	// the resource; this leaves the resource exactly where it was and ends
	// only the attempt, so a client reading "delete" and inferring the
	// record was gone would be inferring wrongly.
	RelCancel LinkRel = "cancel"

	// RelSetInjectors is the affordance that replaces a credential type's
	// injector document, distinct from RelSetInputs and RelUpdate for the
	// same reason each of those is its own: one relation, one endpoint, one
	// control per resource. It is the highest-consequence write a credential
	// type has, since an injector decides what a customer's run executes
	// with, and it still carries credential:write; the store is what refuses
	// a dangerous injector, not the relation.
	RelSetInjectors LinkRel = "set-injectors"

	// RelSetSurvey is the affordance that replaces a template's survey: the
	// questions a launching operator is asked, and whether they are asked
	// at all.
	//
	// Its own relation for the reason RelSetInputs gives, and the shape is
	// the same one: a template already uses RelUpdate for its metadata
	// edit, so a survey control sharing that relation would give one
	// resource two candidates a client cannot tell apart and two controls
	// the UI cannot label separately. It carries the same template:write
	// scope the metadata edit does. The separation identifies the
	// affordance; it does not describe a different privilege.
	//
	// The consequence is worth naming even so. A survey answer becomes an
	// extra variable the automation reads, so editing a survey changes what
	// every future run of that template receives, which is a larger thing
	// than renaming it.
	RelSetSurvey LinkRel = "set-survey"

	// RelCheck is the affordance that runs the resource as a check: every
	// task asked what it would change, and nothing changed.
	//
	// Its own relation rather than RelExecute with a mode in the body, for
	// two reasons. A template already uses RelExecute for its launch, and a
	// relation is unique per resource. And a Controller that predates check
	// mode answers a request to this relation's route with 404, where the
	// same request to the launch route with a mode it had never heard of
	// would have run for real. It carries runbook:check, which
	// runbook:execute implies, so a caller may be granted checks without
	// being granted changes.
	RelCheck LinkRel = "check"

	// RelRollback is the affordance that undoes what a finished job
	// changed. Its own relation because a job already uses RelExecute for
	// its relaunch, and the two are opposites a client must tell apart:
	// one runs the work again, the other takes it back out. It carries
	// runbook:execute, since a rollback changes devices as any run does.
	RelRollback LinkRel = "rollback"

	// RelOnboard is the affordance that onboards a generic device: probe
	// it over its protocol and record what it proved. It carries
	// inventory:onboard.
	RelOnboard LinkRel = "onboard"
)

// Affordance is one candidate action, described purely in authorization
// terms.
//
// It deliberately carries no HTTP method, no URL, and no route pattern. A
// HATEOASGenerator's whole job is to decide which candidates an identity
// may exercise; handing it a URL would let a third-party implementation
// put a URL of its own choosing into a response body. The caller owns the
// method and the href, built from its own route table, and never reads
// either back from a generator.
type Affordance struct {
	// Rel names the relation this candidate would appear under. It is
	// unique per resource, which is what lets the caller correlate a
	// permitted result back to its own method and href.
	Rel LinkRel

	// Scope is the Scope admission must grant before this affordance is
	// offered. It is the same value the matching route declares, read
	// from the same table, so a link can never advertise an action whose
	// scope the router does not actually enforce.
	Scope Scope

	// Target is the PLAN.md Section 18.4 Team/RoleBinding target, when
	// the caller has resolved one. It is the zero value today: no route
	// resolves a ScopeTarget yet, and the field exists so that appending
	// NewScopeRule to the chain later needs no change to this port.
	Target ScopeTarget
}

// HATEOASGenerator answers the one question RBAC-aware hypermedia needs:
// of these candidate affordances, which may this identity exercise right
// now.
//
// The return value is a set of LinkRel, not a set of Affordance, on
// purpose. An implementation may only ever select from what it was
// offered. It cannot widen a Scope, retarget a resource, or introduce a
// relation the router does not serve, because it has no way to express
// any of those. The caller re-intersects the result against its own
// candidate set regardless, so a buggy or hostile implementation cannot
// put an unknown relation into a response body.
type HATEOASGenerator interface {
	Permitted(ctx context.Context, id *Identity, candidates []Affordance) ([]LinkRel, error)
}

// admissionGenerator answers Permitted by asking an AdmissionChain about
// each candidate in turn.
type admissionGenerator struct {
	chain AdmissionChain
}

// NewAdmissionHATEOASGenerator builds the default HATEOASGenerator, the
// one every composition root wires.
//
// It takes an AdmissionChain and not an Admission, and that is the most
// load-bearing decision in this file. Admission.Evaluate (chain.go)
// records every decision through Recorder, Info on allow and Warn on
// deny. Probing N candidates per request through Admission would emit N
// audit lines per request, and every affordance a caller legitimately
// lacks would be logged as a denial at Warn. That is wrong twice: it
// buries the real denials, the ones where somebody actually attempted
// something, under speculative ones nobody attempted; and it changes what
// the Audit Trail means, since nobody asked to delete anything by loading
// a page. api.RequireScope keeps the recorded Admission, so every real
// attempt is still recorded exactly once.
//
// Both consumers must be built from the same AdmissionChain value at the
// composition root. That is what makes "the links agree with the
// enforcement" a structural property rather than a convention: there is
// one rule list, and a link and a 403 cannot disagree because they are
// the same evaluation run against the same rules.
//
// It fails closed at construction on an empty chain, mirroring
// NewJWTEvaluator, NewStaticKeyProvider, and api.NewRouter.
// AdmissionChain.Evaluate denies every request when it holds no rules, so
// an empty chain here would emit an empty link set for every caller
// including an admin: exactly the vacuous pass Phase 13's Adversarial
// gate names, delivered silently rather than as a startup error.
func NewAdmissionHATEOASGenerator(chain AdmissionChain) (HATEOASGenerator, error) {
	if len(chain) == 0 {
		return nil, fmt.Errorf("auth: HATEOAS generator requires a non-empty AdmissionChain; an empty chain denies everything, so every caller would receive an empty link set")
	}
	return &admissionGenerator{chain: chain}, nil
}

// Permitted implements HATEOASGenerator.
//
// A nil identity permits nothing. That is the same fail-closed branch
// scopeRule already takes for an unauthenticated caller, and it matters
// here because an unauthenticated request should never have reached a
// response body in the first place.
//
// Note what this does not do: it never reads id.Scopes directly. Going
// through the chain is what makes Identity.HasScope's admin bypass and
// wildcard scope apply, so a RoleAdmin holding no explicit scopes
// receives every link rather than none. A generator that iterated
// id.Scopes would emit an empty set for exactly the caller who is allowed
// the most, which is the vacuous pass this phase exists to close.
func (g *admissionGenerator) Permitted(ctx context.Context, id *Identity, candidates []Affordance) ([]LinkRel, error) {
	permitted := make([]LinkRel, 0, len(candidates))
	if id == nil {
		return permitted, nil
	}
	for _, candidate := range candidates {
		req := AdmissionRequest{RequiredScope: candidate.Scope, Target: candidate.Target}
		if err := g.chain.Evaluate(ctx, id, req); err != nil {
			// A denial is the ordinary case, not an error: it is the
			// answer "you may not do this," which is precisely what the
			// caller asked for. Only a chain that cannot reach a verdict
			// at all would be an error, and no rule in this repository
			// reports one distinguishably today.
			continue
		}
		permitted = append(permitted, candidate.Rel)
	}
	return permitted, nil
}
