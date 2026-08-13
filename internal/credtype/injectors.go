package credtype

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// The injector document: how a credential's inputs reach a running job.
//
// PLAN.md Section 29.2 defines three targets, one of which has two
// spellings, which is the "four injector targets" the phase checklist
// counts:
//
//	env         environment variables
//	extra_vars  extra variables, including nested structures
//	file        one generated file, or several, addressed back by a
//	            reserved filename variable
//
// Every VALUE is a template over the type's own input ids. No KEY is ever
// templated, in any target. AWX does not template them either, and a
// templated key is an injection surface for nothing anybody needs: an
// environment variable name computed from a secret is not a feature.

// The reserved namespace an injector template uses to address the files it
// generates.
const (
	// ReservedTower is AWX's own spelling. It is supported for import
	// fidelity: a credential type exported from AWX writes
	// "{{ tower.filename }}" and must keep working here unchanged.
	ReservedTower = "tower"

	// ReservedPleiades is the native spelling, offered so a type authored
	// here does not have to name a different product. Both resolve to the
	// same value from one place.
	ReservedPleiades = "pleiades"

	// ReservedFilename is the key beneath either namespace.
	ReservedFilename = "filename"

	// FileTemplateKey is the single-file spelling: a file map holding
	// exactly this key generates one file.
	FileTemplateKey = "template"

	// fileTemplatePrefix begins the multi-file spelling,
	// "template.<label>".
	fileTemplatePrefix = FileTemplateKey + "."
)

// envNamePattern constrains an environment variable name.
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// fileLabelPattern constrains a multi-file label, which becomes part of a
// generated filename.
var fileLabelPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// reservedEnvNames are environment variables an injector may never set.
//
// This is the headline finding of this phase's Schema and Injection
// Hardening audit, and it is worth being explicit about why it is not
// paranoia. PLAN.md Section 29.4 accepts the ephemeral container as the
// trust boundary, which is what permits secrets in the environment at all.
// But the customer's playbook runs INSIDE that boundary, so an injector
// that can set LD_PRELOAD is arbitrary code execution inside the very
// process the credential was meant to authenticate. The container being
// single use does not help: the code runs before the container is
// destroyed.
//
// The rest are variables whose reassignment breaks the adapter rather than
// the host: PATH decides which ansible-playbook runs, and the two Ansible
// color variables are what this platform's own stdout parser depends on.
var reservedEnvNames = map[string]string{
	"LD_PRELOAD":          "loading an arbitrary shared object into every process the playbook starts is code execution inside the run",
	"LD_LIBRARY_PATH":     "redirecting library resolution is code execution inside the run",
	"LD_AUDIT":            "the dynamic linker's audit interface is code execution inside the run",
	"PYTHONPATH":          "Ansible is Python, so this is code execution inside the run",
	"PYTHONHOME":          "as PYTHONPATH",
	"PYTHONSTARTUP":       "as PYTHONPATH",
	"BASH_ENV":            "every non-interactive shell the playbook spawns would source it",
	"ENV":                 "as BASH_ENV, for POSIX shells",
	"IFS":                 "changing the field separator changes how every shell command in the run is parsed",
	"PATH":                "this decides which ansible-playbook actually runs",
	"HOME":                "this decides where ssh looks for its own configuration and known hosts",
	"ANSIBLE_CONFIG":      "an injected ansible.cfg can enable plugins and change the connection plugin",
	"ANSIBLE_FORCE_COLOR": "this platform's own stdout parser depends on its value",
	"ANSIBLE_NOCOLOR":     "as ANSIBLE_FORCE_COLOR",
}

// reservedEnvPrefixes are name prefixes an injector may never use.
var reservedEnvPrefixes = map[string]string{
	// A bash function exported through the environment is executed on
	// shell startup. This is the shape Shellshock exploited.
	"BASH_FUNC_": "an exported shell function is executed by every bash the playbook starts",
}

// Injectors says how a credential's inputs reach a run.
//
// The JSON tags are AWX's own, so an exported injector document decodes
// directly. See the package comment for why that is load bearing.
type Injectors struct {
	// Env maps an environment variable name to a template for its value.
	Env map[string]string `json:"env,omitempty"`

	// ExtraVars maps an extra-variable name to a template, or to a nested
	// map whose leaves are templates. The any is what makes nesting
	// expressible, and nesting is what the release gate's "one nested
	// extra var" means.
	ExtraVars map[string]any `json:"extra_vars,omitempty"`

	// File is either the single-file spelling, one entry keyed
	// "template", or the multi-file spelling, entries keyed
	// "template.<label>". The two are mutually exclusive within one type,
	// which is AWX's own rule.
	File map[string]string `json:"file,omitempty"`
}

// Empty reports whether this document injects nothing at all. A type with
// no injectors is legal: an ssh machine credential injects through a Go
// object rather than through any of these targets.
func (inj Injectors) Empty() bool {
	return len(inj.Env) == 0 && len(inj.ExtraVars) == 0 && len(inj.File) == 0
}

// FileLabels returns the labels of the files this document generates,
// sorted. The single-file spelling reports one empty label, which is what
// distinguishes it from a type generating no files at all.
func (inj Injectors) FileLabels() []string {
	if len(inj.File) == 0 {
		return nil
	}
	if _, single := inj.File[FileTemplateKey]; single {
		return []string{""}
	}

	out := make([]string, 0, len(inj.File))
	for key := range inj.File {
		out = append(out, strings.TrimPrefix(key, fileTemplatePrefix))
	}
	sort.Strings(out)
	return out
}

// Validate reports whether this document is well formed against schema,
// compiling every template through eng.
//
// The check that matters most is the last one: every name a template
// references must be a declared input or the reserved namespace. The
// renderer refuses an unsupplied name at render time, which is correct but
// late, and a launch is the wrong moment to discover a typo in a credential
// type. This moves that failure to the write.
func (inj Injectors) Validate(schema InputSchema, eng render.Engine) error {
	if eng == nil {
		return fmt.Errorf("%w: a render engine is required to validate injector templates", ErrInvalidType)
	}

	known := knownNames(schema)

	for name, tmpl := range inj.Env {
		if err := validateEnvName(name); err != nil {
			return err
		}
		if err := checkTemplate(eng, known, "env "+name, tmpl); err != nil {
			return err
		}
	}

	if err := inj.validateExtraVars(known, eng, nil); err != nil {
		return err
	}

	return inj.validateFile(known, eng)
}

// validateExtraVars walks the extra-variable tree, which may nest.
func (inj Injectors) validateExtraVars(known map[string]bool, eng render.Engine, path []string) error {
	return walkExtraVars(inj.ExtraVars, path, func(where string, tmpl string) error {
		return checkTemplate(eng, known, "extra_vars "+where, tmpl)
	})
}

// walkExtraVars descends a nested extra-variable map, calling fn for each
// leaf template.
func walkExtraVars(vars map[string]any, path []string, fn func(where, tmpl string) error) error {
	for name, value := range vars {
		if name == "" {
			return fmt.Errorf("%w: extra_vars has an entry with an empty name", ErrInvalidType)
		}
		here := append(append([]string{}, path...), name)
		where := strings.Join(here, ".")

		switch v := value.(type) {
		case string:
			if err := fn(where, v); err != nil {
				return err
			}
		case map[string]any:
			if err := walkExtraVars(v, here, fn); err != nil {
				return err
			}
		case nil:
			return fmt.Errorf("%w: extra_vars %q is null, which injects nothing", ErrInvalidType, where)
		default:
			// A number or a boolean written literally in the injector
			// document is legal in AWX and needs no rendering, so it is
			// accepted and passed through untouched rather than refused.
			// Anything else (a list of templates, say) is not something
			// this platform knows how to render, and accepting it would
			// mean injecting a Go-formatted value.
			if !isLiteralScalar(value) {
				return fmt.Errorf("%w: extra_vars %q is a %T, which is not a template, a nested map, or a literal scalar",
					ErrInvalidType, where, value)
			}
		}
	}
	return nil
}

// isLiteralScalar reports whether v is a JSON scalar that needs no
// rendering.
func isLiteralScalar(v any) bool {
	switch v.(type) {
	case bool, float64, float32, int, int64:
		return true
	}
	return false
}

// validateFile checks the file target, including the mutual exclusion
// between its two spellings.
func (inj Injectors) validateFile(known map[string]bool, eng render.Engine) error {
	if len(inj.File) == 0 {
		return nil
	}

	_, single := inj.File[FileTemplateKey]
	multi := false
	for key := range inj.File {
		if strings.HasPrefix(key, fileTemplatePrefix) {
			multi = true
		}
	}

	// AWX's own rule. The two spellings mean different things about how
	// the reserved variable resolves ({{ tower.filename }} versus
	// {{ tower.filename.cert }}), so a document using both has no
	// consistent reading and guessing one would silently generate the
	// wrong number of files.
	if single && multi {
		return fmt.Errorf(
			"%w: the file injector mixes the single-file key %q with multi-file %q keys, and the two are mutually exclusive",
			ErrInvalidType, FileTemplateKey, fileTemplatePrefix+"<label>")
	}

	for key, tmpl := range inj.File {
		if key != FileTemplateKey {
			if !strings.HasPrefix(key, fileTemplatePrefix) {
				return fmt.Errorf(
					"%w: the file injector key %q must be %q or %q",
					ErrInvalidType, key, FileTemplateKey, fileTemplatePrefix+"<label>")
			}
			label := strings.TrimPrefix(key, fileTemplatePrefix)
			if !fileLabelPattern.MatchString(label) {
				return fmt.Errorf(
					"%w: the file injector label %q must be lowercase letters, digits and underscores, since it becomes part of a filename",
					ErrInvalidType, label)
			}
		}
		if err := checkTemplate(eng, known, "file "+key, tmpl); err != nil {
			return err
		}
	}
	return nil
}

// validateEnvName checks one environment variable name.
func validateEnvName(name string) error {
	if !envNamePattern.MatchString(name) {
		return fmt.Errorf(
			"%w: the environment variable name %q must be letters, digits and underscores, not starting with a digit",
			ErrInvalidType, name)
	}
	if why, reserved := reservedEnvNames[name]; reserved {
		return fmt.Errorf("%w: an injector may not set %s, because %s", ErrInvalidType, name, why)
	}
	for prefix, why := range reservedEnvPrefixes {
		if strings.HasPrefix(name, prefix) {
			return fmt.Errorf("%w: an injector may not set a %s* variable, because %s", ErrInvalidType, prefix, why)
		}
	}
	return nil
}

// checkTemplate compiles one template and checks every name it references.
func checkTemplate(eng render.Engine, known map[string]bool, where, source string) error {
	tmpl, err := eng.Compile(source)
	if err != nil {
		return fmt.Errorf("%w: the %s template is not valid: %s", ErrInvalidType, where, err)
	}
	for _, name := range tmpl.Names() {
		if !known[name] {
			return fmt.Errorf(
				"%w: the %s template references %q, which is neither a declared input nor the reserved %q namespace",
				ErrInvalidType, where, name, ReservedTower)
		}
	}
	return nil
}

// knownNames is every top-level name an injector template may reference:
// the type's own input ids, plus the two spellings of the reserved
// namespace.
func knownNames(schema InputSchema) map[string]bool {
	known := map[string]bool{
		ReservedTower:    true,
		ReservedPleiades: true,
	}
	for _, id := range schema.IDs() {
		known[id] = true
	}
	return known
}

// Preview is the SHAPE an injector document would produce: which
// environment variables, which extra-variable keys, which files. It carries
// no values.
//
// It exists so an author can find a miswritten injector before a run does.
// Reporting only the shape is what makes that safe to expose: the endpoint
// behind it renders against caller-supplied dummy values and touches no
// stored credential, so there is nothing to disclose, and returning the
// rendered values anyway would turn an authoring aid into an oracle.
type Preview struct {
	// Env are the environment variable names, sorted.
	Env []string

	// ExtraVars are the extra-variable keys, dotted for nested ones,
	// sorted.
	ExtraVars []string

	// FileLabels are the generated file labels, sorted. A single empty
	// entry means the single-file spelling.
	FileLabels []string
}

// Preview renders every template against inputs and reports the shape of
// what would be produced.
//
// Rendering really happens, and a failure is returned, which is the point:
// a preview that only listed keys without evaluating anything would report
// success for a document that cannot render. The rendered values are then
// discarded.
func (inj Injectors) Preview(schema InputSchema, eng render.Engine, inputs map[string]string) (Preview, error) {
	if eng == nil {
		return Preview{}, fmt.Errorf("%w: a render engine is required to preview injectors", ErrInvalidType)
	}

	vars := make(map[string]any, len(inputs)+2)
	for id, v := range inputs {
		vars[id] = v
	}
	// Defaults fill in what the caller did not supply, so a preview does
	// not report an undefined-variable failure for an input the type
	// itself would have answered.
	for id, v := range schema.Defaults() {
		if _, supplied := vars[id]; !supplied {
			vars[id] = v
		}
	}

	// The reserved namespace, with placeholder paths. A real injection
	// computes these from the credential's own id, which a preview does
	// not have and does not need: a template referencing tower.filename
	// must resolve to SOMETHING for the preview to prove it renders.
	filenames := make(map[string]any, len(inj.File)+1)
	for _, label := range inj.FileLabels() {
		if label == "" {
			// The single-file spelling addresses the whole namespace as a
			// value rather than as a map, so the two shapes cannot both be
			// represented at once. They are mutually exclusive anyway.
			vars[ReservedTower] = map[string]any{ReservedFilename: previewFilePath}
			vars[ReservedPleiades] = vars[ReservedTower]
			filenames = nil
			break
		}
		filenames[label] = previewFilePath + "." + label
	}
	if filenames != nil {
		reserved := map[string]any{ReservedFilename: filenames}
		vars[ReservedTower] = reserved
		vars[ReservedPleiades] = reserved
	}

	var out Preview

	for name, tmpl := range inj.Env {
		if err := renderForPreview(eng, vars, "env "+name, tmpl); err != nil {
			return Preview{}, err
		}
		out.Env = append(out.Env, name)
	}

	if err := walkExtraVars(inj.ExtraVars, nil, func(where, tmpl string) error {
		if err := renderForPreview(eng, vars, "extra_vars "+where, tmpl); err != nil {
			return err
		}
		out.ExtraVars = append(out.ExtraVars, where)
		return nil
	}); err != nil {
		return Preview{}, err
	}

	for key, tmpl := range inj.File {
		if err := renderForPreview(eng, vars, "file "+key, tmpl); err != nil {
			return Preview{}, err
		}
	}
	out.FileLabels = inj.FileLabels()

	sort.Strings(out.Env)
	sort.Strings(out.ExtraVars)
	return out, nil
}

// previewFilePath stands in for a path a real injection computes from the
// credential's own id. It is obviously not a real path, so a reader cannot
// mistake a preview for a description of a run.
const previewFilePath = "/run/pleiades/credentials/<preview>"

// renderForPreview renders one template and discards the result, reporting
// a failure with enough context to fix it and without quoting any value.
func renderForPreview(eng render.Engine, vars map[string]any, where, source string) error {
	compiled, err := eng.Compile(source)
	if err != nil {
		return fmt.Errorf("%w: the %s template is not valid: %s", ErrInvalidType, where, err)
	}
	if _, err := compiled.Render(vars); err != nil {
		return fmt.Errorf("%w: the %s template could not render: %s", ErrInvalidType, where, err)
	}
	return nil
}
