package credtype

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// The injector: the one place a set of resolved credentials becomes the
// material a run executes with.
//
// It does no I/O of its own, deliberately. It resolves no external secret
// (the resolver did that), reads no database row (the store did that),
// writes no file and opens no connection (the adapter does that). What it
// does is render, and rendering is a pure function of the credential and
// its type. That is what makes the whole thing testable against real
// documents rather than against a mock of something.

// Injector renders resolved credentials into the artifact a dispatch
// injects.
//
// Safe for concurrent use: the render engine's cache is, the Literals set
// is, and the Targets hold no state.
type Injector struct {
	engine   render.Engine
	targets  []Target
	literals *redact.Literals
}

// InjectorOption configures an Injector.
type InjectorOption func(*Injector)

// WithLiterals sets where rendered secrets are registered for masking.
//
// It exists for a test that needs an isolated set. Production leaves it at
// the default, which is the process-wide set redact.Shared owns, because a
// secret registered with one set and logged through another is not masked
// and the failure is silent (see redact.Shared's own doc comment).
func WithLiterals(l *redact.Literals) InjectorOption {
	return func(in *Injector) { in.literals = l }
}

// NewInjector builds an Injector over a render engine.
//
// It refuses a nil engine rather than reaching for a package-level default,
// and that refusal is one of the three mechanisms proving PLAN.md Section
// 25's one-renderer rule: there is no way to obtain an Injector that
// compiles templates with something other than the engine the caller
// handed it. The other two are internal/archtest's
// TestExactlyOneRendererImplementation and internal/render's own shared
// call-site ledger.
func NewInjector(eng render.Engine, opts ...InjectorOption) (*Injector, error) {
	if eng == nil {
		return nil, fmt.Errorf("%w: an injector requires a render engine", ErrInjection)
	}

	in := &Injector{engine: eng, literals: redact.Shared().Literals()}
	for _, opt := range opts {
		opt(in)
	}

	// Every Target is wrapped, with no way for a caller to opt out. See
	// secretTracking's own doc comment for why the wrapping is the control
	// rather than a convenience.
	for _, t := range orderedTargets() {
		in.targets = append(in.targets, secretTracking{inner: t, literals: in.literals})
	}
	return in, nil
}

// PromptedInputs are the credential input values a launch was asked for at
// run time, keyed by credential id and then by input id.
//
// # The never-persist rule, as a type rather than as a discipline
//
// These deliberately do not travel through launch.Config. That type is
// layered-precedence machinery (Saved, Overrides, Answers), and a prompted
// credential input has no layers by construction: it is typed once, used
// once, and must never be stored. Threading it through Config would create
// a Saved slot for a value that must never be saved, and the only thing
// stopping it from being written there would be somebody remembering.
//
// So it is a separate parameter everywhere it travels, and internal/api's
// own recordConfig, which is what writes a launch configuration to the
// database, cannot see it. Not "does not read it": cannot see it, because
// it is not in the value it is handed. That is the never-persist rule made
// structural, and internal/api has a test saying so out loud, because
// "there is nothing to redact" is a claim and claims get tests.
type PromptedInputs map[int]map[string]string

// Inject renders every credential and combines the results.
//
// prompted supplies the values a launch was asked for at run time. They are
// merged over the stored values without being written to them.
//
// Order matters and is preserved: the caller resolved the credentials in
// binding order, and vault identities reach a playbook as ordered
// --vault-id arguments.
func (in *Injector) Inject(creds []Credential, prompted PromptedInputs) (Artifact, error) {
	if len(creds) == 0 {
		return Artifact{}, nil
	}

	arts := make([]Artifact, 0, len(creds))
	for _, cred := range creds {
		art, err := in.InjectOne(cred, prompted[cred.ID])
		if err != nil {
			return Artifact{}, err
		}
		arts = append(arts, art)
	}
	return Combine(arts...)
}

// InjectOne renders exactly one credential.
//
// It validates first, against the credential's own type, which is what
// turns a missing required input into an error naming the input rather than
// into an undefined-variable failure naming a template.
func (in *Injector) InjectOne(cred Credential, prompted map[string]string) (Artifact, error) {
	cred = withPrompted(cred, prompted)

	if err := cred.Validate(); err != nil {
		return Artifact{}, fmt.Errorf("%w: credential %q: %s", ErrInjection, cred.Name, err)
	}

	art := Artifact{CredentialID: cred.ID, CredentialName: cred.Name}
	req := Request{Credential: cred, Engine: in.engine, Vars: cred.RenderVars()}

	phase := PhaseFiles
	for _, target := range in.targets {
		if target.Phase() != phase {
			// Crossing into PhaseValues: the file target has decided every
			// path, so the reserved namespace can now be built. Doing it
			// here rather than inside a Target is what keeps the two
			// phases' contract in one readable place.
			phase = target.Phase()
			addReservedNamespace(req.Vars, cred)
		}
		if err := target.Apply(req, &art); err != nil {
			return Artifact{}, err
		}
	}

	sort.Slice(art.Files, func(i, j int) bool { return art.Files[i].Path < art.Files[j].Path })
	return art, nil
}

// withPrompted returns a copy of cred with prompted values merged over its
// stored ones.
//
// A copy rather than a mutation because the caller's map may be the one the
// resolver handed out, and writing a prompted password into it would leave
// a value nobody stored attached to something that outlives this call.
//
// An empty prompted value is skipped rather than written, so an operator
// who left a prompt blank gets the stored value or the required-input
// error, not a silent empty credential.
func withPrompted(cred Credential, prompted map[string]string) Credential {
	if len(prompted) == 0 {
		return cred
	}
	merged := make(map[string]string, len(cred.Inputs)+len(prompted))
	for id, v := range cred.Inputs {
		merged[id] = v
	}
	for id, v := range prompted {
		if v != "" {
			merged[id] = v
		}
	}
	cred.Inputs = merged
	return cred
}

// addReservedNamespace binds the generated files' paths under both
// spellings of the reserved namespace, so an env or extra_vars template can
// address a file the file target just decided on.
//
// The two spellings share one value rather than each being built, which is
// what makes "{{ tower.filename }}" and "{{ pleiades.filename }}"
// necessarily identical instead of merely intended to be.
//
// The single-file spelling binds a value and the multi-file spelling binds
// a map, which is why they are mutually exclusive within one type and why
// Injectors.Validate refuses a document mixing them: the same dotted path
// would have to resolve to two different shapes.
func addReservedNamespace(vars map[string]any, cred Credential) {
	labels := cred.Type.Injectors.FileLabels()
	if len(labels) == 0 {
		return
	}

	var reserved map[string]any
	if len(labels) == 1 && labels[0] == "" {
		reserved = map[string]any{ReservedFilename: FilePath(cred.ID, "")}
	} else {
		filenames := make(map[string]any, len(labels))
		for _, label := range labels {
			filenames[label] = FilePath(cred.ID, label)
		}
		reserved = map[string]any{ReservedFilename: filenames}
	}

	vars[ReservedTower] = reserved
	vars[ReservedPleiades] = reserved
}

// renderOne compiles and renders a single template.
//
// The error names where the template lives and what the renderer said, and
// never the values it was rendering against. It is returned to a dispatch
// path that writes a reason onto a job record, and half of those values are
// secrets. render.UndefinedError is built to the same rule: it carries a
// name and never a value.
func renderOne(req Request, where, source string) (string, error) {
	tmpl, err := req.Engine.Compile(source)
	if err != nil {
		return "", fmt.Errorf("%w: credential %q: the %s template is not valid: %s",
			ErrInjection, req.Credential.Name, where, err)
	}
	value, err := tmpl.Render(req.Vars)
	if err != nil {
		return "", fmt.Errorf("%w: credential %q: the %s template could not render: %s",
			ErrInjection, req.Credential.Name, where, err)
	}
	return value, nil
}

// renderVarTree renders a nested extra-variable document, returning a tree
// of the same shape with every template leaf replaced by its rendered
// value.
//
// A literal scalar (a number or a boolean written directly into the
// injector document) passes through untouched, because it is already the
// value and rendering it would turn it into a string. Injectors.Validate
// accepts exactly those two non-template leaf shapes, so anything else here
// is unreachable for a validated document and is reported rather than
// coerced.
func renderVarTree(req Request, vars map[string]any, path []string) (map[string]any, error) {
	out := make(map[string]any, len(vars))
	for _, name := range sortedKeys(vars) {
		here := append(append([]string{}, path...), name)
		where := strings.Join(here, ".")

		switch v := vars[name].(type) {
		case string:
			value, err := renderOne(req, "extra_vars "+where, v)
			if err != nil {
				return nil, err
			}
			out[name] = value
		case map[string]any:
			nested, err := renderVarTree(req, v, here)
			if err != nil {
				return nil, err
			}
			out[name] = nested
		default:
			if !isLiteralScalar(vars[name]) {
				return nil, fmt.Errorf("%w: credential %q: extra_vars %q is a %T, which is not a template, a nested map, or a literal scalar",
					ErrInjection, req.Credential.Name, where, vars[name])
			}
			out[name] = vars[name]
		}
	}
	return out, nil
}
