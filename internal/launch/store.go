package launch

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// ErrNotFound is returned when an id names no template or saved
// configuration.
var ErrNotFound = errors.New("launch: record not found")

// ErrExists is returned when a template name is already taken within its
// organization. A caller's mistake, which is what makes it a 409 rather
// than a 500.
var ErrExists = errors.New("launch: a template with that name already exists in this organization")

// ErrCrossTenant is returned when a template names an inventory belonging
// to a different organization than the template itself.
//
// Refused at the write, and this is the most security-relevant check in the
// package. A template is a saved instruction to run something against a set
// of hosts; if its organization and its inventory could disagree, a caller
// granted admin over one tenant could author a template in that tenant that
// dispatches against another tenant's fleet. Every individual step would
// pass its own check, which is exactly the shape FAILURE_PATTERNS.md #97
// records.
var ErrCrossTenant = errors.New("launch: template and inventory belong to different organizations")

// ErrInUse is returned when a template cannot be deleted because something
// still points at it.
//
// The only thing that can today is a Schedule, whose edge is deliberately
// NOT cascaded: a schedule is an independent object an operator created and
// can see in its own list, so deleting a template out from under one should
// be refused rather than silently stop automation somebody relies on. The
// deletion attempt is the moment to say so.
//
// It exists as a typed error rather than being left to the database's own
// constraint failure because that failure reaches an HTTP handler as an
// opaque 500, which tells an operator that the server is broken when in
// fact they asked for something reasonable that is being refused for a
// reason they can act on.
var ErrInUse = errors.New("launch: template is still referenced")

// Query is a list request over templates.
type Query struct {
	// After is a keyset cursor: the highest id already seen.
	After int

	// Limit bounds the page. Zero means the store's default.
	Limit int

	// Search narrows by name, case insensitively.
	Search string

	// OrganizationIDs restricts to particular tenants. Empty means no
	// restriction, which is the caller's decision rather than this port's:
	// authorization is the admission chain's job, and a store that
	// filtered on its own would be a second place answering it.
	OrganizationIDs []int
}

// Store persists templates and their saved launch configurations.
//
// One port rather than two, because a saved configuration has no life of
// its own: it belongs to exactly one template, cascades with it, and is
// only meaningful against that template's declared prompts. A caller
// holding one already holds the other.
type Store interface {
	// Create persists a new template. It returns ErrExists on a duplicate
	// name within the organization, and ErrCrossTenant when the named
	// inventory belongs to somebody else.
	Create(ctx context.Context, tmpl Template) (Template, error)

	// Get loads one template with its survey.
	Get(ctx context.Context, id int) (Template, error)

	// List returns a page of templates, oldest id first.
	List(ctx context.Context, q Query) ([]Template, error)

	// Update saves a template's name, description, defaults, prompts,
	// survey and flags.
	//
	// The kind and the definition are not updatable, and that is a
	// deliberate narrowing rather than an omission. Re-pointing a template
	// at different code while it keeps its name, its access grants and its
	// job history is how a reviewed thing quietly becomes an unreviewed
	// one. Changing what runs means creating a template, which leaves two
	// legible records instead of one silent change, and Copy is what makes
	// that cheap.
	Update(ctx context.Context, tmpl Template) error

	// Delete removes a template. Its survey questions and saved
	// configurations go with it; the jobs it launched do not, because a
	// job is a historical record and "what did this template run" is
	// precisely the question somebody has once it is gone.
	Delete(ctx context.Context, id int) error

	// SavedConfigs returns the stored launch configurations for a
	// template, newest last.
	SavedConfigs(ctx context.Context, templateID int) ([]SavedConfig, error)

	// SaveConfig stores one launch configuration against a template.
	SaveConfig(ctx context.Context, cfg SavedConfig) (SavedConfig, error)

	// GetConfig loads one stored launch configuration.
	GetConfig(ctx context.Context, id int) (SavedConfig, error)
}

// SavedConfig is a stored bundle of launch-time overrides.
//
// PLAN.md Section 28 names it as what schedules and workflow nodes attach
// to. Neither exists yet, so it ships with the one consumer that does:
// relaunch, which reuses what a job ran with rather than asking somebody to
// remember what they typed. Shipping it with no consumer at all is the
// built-but-unreachable failure this repository has recorded three times.
type SavedConfig struct {
	ID         int
	TemplateID int

	// Name is what a reader picks it by. Empty for the anonymous
	// configuration a relaunch stores against one job.
	Name string

	// Fields and Answers are the two halves of a launch.Config, in the
	// same sparse shape: absent means "not supplied", never "set to
	// empty".
	Fields  Fields
	Answers map[string]any
}

// Config projects a saved configuration onto the shape Resolve folds.
func (c SavedConfig) Config() Config {
	return Config{Saved: c.Fields, Answers: c.Answers}
}

// Redact replaces every secret answer with a marker, for a projection that
// leaves this process.
//
// Encryption at rest and redaction on the wire are separate controls for
// separate exposures, and one does not cover the other: a stored answer is
// encrypted against somebody reading the database, and redacted against
// somebody reading the API. A caller who may edit a template may not
// thereby read the vault token an operator typed into it last week.
//
// The survey decides which variables are secret, so this takes one rather
// than guessing from the value. A projection that guessed would be a second
// place deciding what a password is.
func (c SavedConfig) Redact(survey Survey) SavedConfig {
	if len(c.Answers) == 0 {
		return c
	}

	redacted := make(map[string]any, len(c.Answers))
	for name, value := range c.Answers {
		redacted[name] = value
	}
	for _, name := range survey.SecretVariables() {
		if _, supplied := redacted[name]; supplied {
			redacted[name] = RedactedMarker
		}
	}

	c.Answers = redacted
	return c
}

// RedactedMarker is what a secret answer reads as on the way out.
//
// A marker rather than an empty string, because empty and withheld are
// different facts: one says nobody answered the question, the other says
// somebody did and you may not see it. A form rendering the empty string
// would also silently clear the stored answer on the next save.
//
// It is an alias of redact.Marker rather than a second spelling of the same
// literal. Phase 22 gave credentials a redaction marker too, and two
// packages each declaring "$encrypted$" is how the two drift: an API client
// comparing against one would silently stop recognizing the other.
const RedactedMarker = redact.Marker
