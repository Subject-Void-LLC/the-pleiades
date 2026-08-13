package access

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// This file holds the accountability half of the access surface: who owns a
// tenant or a team, how to reach them, and the dated statement that somebody
// checked.
//
// The two are one subject rather than two. A contact list nobody has
// confirmed is a liability dressed as a control: it decays silently, because
// nothing breaks when an escalation number stops working right up until the
// moment it is needed, and a stale entry is worse than an empty one because
// it stops anybody looking further. The attestation is what turns "we hold
// this information" into "we know it is true", and only the second is a
// claim worth making to an auditor.

// ContactRole names what a contact is for.
//
// A closed vocabulary, for the reason auth.Scope and view.NavGroup are: the
// role decides who gets woken up, and a free-text role means two
// organizations spelling "escalation" two ways and a query that finds
// neither.
type ContactRole string

const (
	// ContactOwner is accountable for the thing existing at all: whether it
	// should still exist, and whether its permissions are proportionate.
	// This is the role an access review asks about.
	ContactOwner ContactRole = "owner"

	// ContactEscalation is who to wake. Distinct from the owner on purpose,
	// because the person accountable for a tenant is frequently not the
	// person who can act on it at 3am, and conflating them produces a page
	// to somebody who can only forward it.
	ContactEscalation ContactRole = "escalation"

	// ContactSecurity is who hears about a suspected compromise, which in a
	// classified deployment is a different path with a different urgency
	// from an outage.
	ContactSecurity ContactRole = "security"

	// ContactBilling is who owns the spend. Present because a tenant with
	// no billing owner is the one nobody decommissions.
	ContactBilling ContactRole = "billing"
)

// contactRoles is the vocabulary, as a set, so validation and the form's
// options read from one place.
var contactRoles = map[ContactRole]bool{
	ContactOwner:      true,
	ContactEscalation: true,
	ContactSecurity:   true,
	ContactBilling:    true,
}

// ContactRoles returns every valid role in the order they are offered,
// which is the order they matter in during an incident rather than
// alphabetical.
func ContactRoles() []ContactRole {
	return []ContactRole{ContactOwner, ContactEscalation, ContactSecurity, ContactBilling}
}

// Classification is a tenant's own marking.
//
// Only the six classification markings, deliberately. view.BannerLevel also
// carries development, staging and production, and those are facts about an
// installation rather than about a tenant inside one: an organization is not
// "staging", the deployment is. Reusing that type here would make the wider
// vocabulary assignable and produce a tenant marked with an environment.
type Classification string

const (
	ClassificationUnclassified Classification = "unclassified"
	ClassificationCUI          Classification = "cui"
	ClassificationConfidential Classification = "confidential"
	ClassificationSecret       Classification = "secret"
	ClassificationTopSecret    Classification = "topsecret"
	ClassificationTopSecretSCI Classification = "topsecret-sci"
)

// classifications is the vocabulary as a set. Empty is valid and means
// unmarked, which is not the same as unclassified: one is an absent
// statement and the other is a statement.
var classifications = map[Classification]bool{
	ClassificationUnclassified: true,
	ClassificationCUI:          true,
	ClassificationConfidential: true,
	ClassificationSecret:       true,
	ClassificationTopSecret:    true,
	ClassificationTopSecretSCI: true,
}

// Classifications returns every valid marking, least sensitive first, which
// is the order a control offers them in.
func Classifications() []Classification {
	return []Classification{
		ClassificationUnclassified,
		ClassificationCUI,
		ClassificationConfidential,
		ClassificationSecret,
		ClassificationTopSecret,
		ClassificationTopSecretSCI,
	}
}

// Contact is a named human or rota accountable for one organization or one
// team.
//
// OrganizationID and TeamID are the owner, and exactly one is set. That is
// checked at the write rather than assumed, for the same reason
// Binding.ScopeID is (FAILURE_PATTERNS.md #99): a nullable reference nothing
// validates becomes a row that belongs to everything or to nothing, and both
// readings are wrong.
type Contact struct {
	ID   int
	Name string
	Role ContactRole

	// Every channel is optional individually and at least one is required,
	// because which ones exist varies: an air-gapped site may have a desk
	// phone and no reachable email, and a rota may be a URL with neither.
	Email string
	Phone string
	URL   string

	// Notes carries the conditions under which this is the right contact.
	// Escalation information that is only a phone number is escalation
	// information somebody will misuse.
	Notes string

	// Order decides who is tried first, which is the entire point of an
	// escalation path and cannot be inferred from a role or a timestamp.
	Order int

	OrganizationID int
	TeamID         int

	// OrganizationName and TeamName are the owner resolved to something a
	// reader can act on, whichever of the two is set.
	//
	// They are free: every contact query already eager-loads its owner in
	// order to know which of the two it is, and the hydrator was reading
	// the id off the loaded row and discarding the name. Carrying them on
	// the domain type rather than looking them up in each view is the rule
	// Binding.TeamName follows, for the reason the store is where the row
	// already is.
	OrganizationName string
	TeamName         string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// OwnedByOrganization reports whether this contact hangs off an organization
// rather than a team.
func (c Contact) OwnedByOrganization() bool { return c.OrganizationID > 0 }

// Owner renders which record this contact belongs to, in words.
//
// The name where it is known, because "organization 7" makes the reader do
// the join and then asks them to remember the answer. This used to render
// exactly that and argued it was readable, which was true and beside the
// point: the id is only readable in the sense that it is not a blank.
//
// An unhydrated contact falls back to the id rather than to a blank, and
// says nothing about the owner being gone. A contact's owner cannot dangle
// (the edge cascades, so deleting an organization deletes its contacts), so
// unlike a role binding's scope target there is no deleted case to
// distinguish. What this fallback marks is a value that was built by hand
// rather than read from storage.
func (c Contact) Owner() string {
	if c.OwnedByOrganization() {
		return "organization " + ownerLabelOrID(c.OrganizationName, c.OrganizationID)
	}
	return "team " + ownerLabelOrID(c.TeamName, c.TeamID)
}

func ownerLabelOrID(name string, id int) string {
	if name != "" {
		return name
	}
	return strconv.Itoa(id)
}

// Attestation is the dated statement that somebody confirmed a record's
// ownership information is current.
//
// The subject is never taken from a submitted form, the same rule an
// announcement's author follows: an attestation somebody could type another
// person's name into is not an attestation, it is a rumour with a date on
// it.
type Attestation struct {
	By string
	At *time.Time
}

// Attested reports whether the record has ever been attested.
func (a Attestation) Attested() bool { return a.At != nil && a.By != "" }

// Describe renders an attestation in words, for a column a reader can scan.
//
// "Never attested" rather than an empty cell, deliberately. A blank is
// ambiguous between "nobody has confirmed this" and "this view does not show
// that", and the first is a finding an access review exists to produce. It
// follows Contact.Owner in rendering here rather than in each view, so two
// pages cannot word the same fact differently.
func (a Attestation) Describe() string {
	if !a.Attested() {
		return "Never attested"
	}
	return a.By + " on " + a.At.UTC().Format("2006-01-02")
}

// Stale reports whether an attestation is older than within, or absent
// entirely. Absent counts as stale: never having been checked is not a
// better position than having been checked too long ago.
//
// The period is the caller's, not a constant here, because how often
// ownership must be re-confirmed is a policy question that differs between a
// commercial tenant and a classified one, and baking one answer in would
// make the stricter deployment silently non-compliant.
func (a Attestation) Stale(now time.Time, within time.Duration) bool {
	if !a.Attested() {
		return true
	}
	return now.Sub(*a.At) > within
}

// ContactQuery is a list request against Contacts.
//
// It names an owner rather than defaulting to every contact in the
// deployment, because there is no reader who wants that: a contact is only
// meaningful beside the thing it is accountable for.
type ContactQuery struct {
	Query

	// OrganizationID and TeamID narrow to one owner. Setting neither lists
	// every contact, which the management view uses and nothing else
	// should.
	OrganizationID int
	TeamID         int
}

// validateContact refuses a contact that could not be acted on.
//
// Three conditions, each of which produced a real failure somewhere before
// it was checked: a contact with no owner, a contact with two owners, and a
// contact with no way to reach anybody.
func validateContact(c Contact) error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("%w: a contact needs a name", ErrInvalidInput)
	}
	if !contactRoles[c.Role] {
		return fmt.Errorf("%w: %q is not a contact role", ErrInvalidInput, c.Role)
	}

	switch {
	case c.OrganizationID > 0 && c.TeamID > 0:
		return fmt.Errorf("%w: a contact is accountable for one record, but this one names organization %d and team %d",
			ErrInvalidInput, c.OrganizationID, c.TeamID)
	case c.OrganizationID <= 0 && c.TeamID <= 0:
		return fmt.Errorf("%w: a contact must name the organization or team it is accountable for", ErrInvalidInput)
	}

	// At least one channel. A contact nobody can reach records that
	// somebody is responsible without recording how to tell them, which is
	// the failure this whole entity exists to prevent.
	if strings.TrimSpace(c.Email) == "" && strings.TrimSpace(c.Phone) == "" && strings.TrimSpace(c.URL) == "" {
		return fmt.Errorf("%w: a contact needs at least one of an email, a phone number or a URL", ErrInvalidInput)
	}
	return nil
}

// ValidClassification reports whether c is the empty marking or one of the
// six real ones.
//
// Exported so a form can mark the offending control rather than refusing the
// whole page, and reading the same table the store validates against: two
// vocabularies would diverge the first time one gained a marking.
func ValidClassification(c Classification) bool {
	return c == "" || classifications[c]
}

// validateClassification accepts the empty marking and the six real ones.
func validateClassification(c Classification) error {
	if ValidClassification(c) {
		return nil
	}
	return fmt.Errorf("%w: %q is not a classification marking", ErrInvalidInput, c)
}
