package collection

// Param documents one key a Collection method reads from its task's
// params map. It exists so a generated reference page can render a real
// parameter table without anyone hand-maintaining a second copy of it.
type Param struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Required    bool     `json:"required,omitempty"`
	Default     string   `json:"default,omitempty"`
	Choices     []string `json:"choices,omitempty"`
	Description string   `json:"description"`

	// Format says where the parameter's text lands when that matters to a
	// rendered value (Phase 117a): on a command line, or as a URL. A
	// runbook that renders into such a parameter is held to a rule for it
	// at validation (internal/validate's template rule). Empty for every
	// other parameter.
	Format ParamFormat `json:"format,omitempty"`
}

// ParamFormat is where a parameter's text lands.
type ParamFormat string

const (
	// ParamFormatCommand is text a shell or a network device's command
	// line reads. An expression rendered into it must end with the quote
	// filter (a POSIX shell) or cli_token (a network CLI, which has no
	// quoting), so data cannot add a command or an argument.
	ParamFormatCommand ParamFormat = "command"
	// ParamFormatURL is a URL. The text before its first expression must
	// already name the scheme and host, or begin with "/" for a path on a
	// device's API, so data cannot choose where a request goes.
	ParamFormatURL ParamFormat = "url"
)

// ReturnField documents one key a Collection method emits, either as a
// fact (sdk.RunbookContext.EmitFact) or a stat.
type ReturnField struct {
	Name string `json:"name"`
	Type string `json:"type"`

	// Returned states when this field is present, e.g. "always" or "on
	// success". Free text rather than an enum: the real conditions
	//("only when page_size is reached", "only for a managed device")
	// do not collapse into a small fixed set.
	Returned string `json:"returned,omitempty"`

	Sample      string `json:"sample,omitempty"`
	Description string `json:"description"`
}

// Example is one paste-ready runbook task stanza demonstrating this
// method, rendered verbatim onto a generated reference page.
type Example struct {
	Name        string `json:"name"`
	RunbookYAML string `json:"runbookYaml"`
}

// Doc is a Collection method's human-facing reference documentation:
// everything the documentation generator (Part XIV, Phase 68) needs to
// emit a per-FQCN reference page with no other input.
//
// It is deliberately a separate struct from the rest of Manifest, not
// flattened into it: Manifest already answers what a method needs from a
// device, and Doc answers what a reader needs to understand and call it,
// two different audiences (the plan-time resolver and a human) reading
// two different sets of fields for two different reasons.
//
// A declared method carries a Summary and nothing else; there is no
// reachable, real behavior yet for Params or Returns to describe, and
// Examples must stay empty rather than fabricate a call that cannot run.
// An implemented method is expected to carry the full contract: this is
// enforced by the documentation generation pipeline's own completeness
// gate, not by this type, since a struct cannot require "this field is
// set if that other field has a particular value."
type Doc struct {
	// Summary is one sentence, no more than 140 characters, describing
	// what this method does. It is what a namespace index page shows
	// next to the FQCN, for both a declared and an implemented method.
	Summary string `json:"summary,omitempty"`

	// Description is longer prose for the method's own reference page.
	// Falls back to Summary when empty.
	Description string `json:"description,omitempty"`

	// SinceVersion names the release this method first shipped in.
	// Empty for everything today: nothing has shipped a tagged release
	// yet.
	SinceVersion string `json:"sinceVersion,omitempty"`

	// Deprecated, when non-empty, is why this method is deprecated and
	// what to use instead.
	Deprecated string `json:"deprecated,omitempty"`

	// Params documents every key this method reads directly, beyond
	// whatever a named Fragment already covers.
	Params []Param `json:"params,omitempty"`

	// Fragments names shared Param sets (see Fragment) this method's
	// own Params should be read alongside. A generated reference page
	// renders a fragment's params inline under its own method, exactly
	// as documented once, wherever it is referenced: the same reuse
	// Ansible's extends_documentation_fragment gives module authors,
	// so a repeated credential or connection parameter is written once.
	Fragments []string `json:"fragments,omitempty"`

	// Returns documents every fact or stat this method emits.
	Returns []ReturnField `json:"returns,omitempty"`

	// Examples are paste-ready runbook task stanzas. Left empty for a
	// declared method by convention: see this type's own doc comment.
	Examples []Example `json:"examples,omitempty"`

	// SeeAlso names other FQCNs a reader of this page probably also
	// wants.
	SeeAlso []string `json:"seeAlso,omitempty"`
}

// Fragment is a named, reusable set of Params, referenced by name from
// one or more methods' Doc.Fragments so a repeated parameter (a shared
// credential shape, a shared pagination knob) is documented in exactly
// one place. Fragment itself carries no name: the name is the key a
// Fragment is registered under, the same shape pkg/capability's Name and
// this package's own Descriptor.Name already establish for "the registry
// key is the identity, not a field inside the value."
type Fragment struct {
	Params []Param
}
