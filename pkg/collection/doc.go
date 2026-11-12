package collection

// Param documents one key a Collection method reads from its task's
// params map. It exists so a generated reference page can render a real
// parameter table without anyone hand-maintaining a second copy of it.
type Param struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
	Default  string `json:"default,omitempty"`

	// Choices, when set, is the closed set of values the parameter takes
	// (Phase 131's R2): pleiades doc and the reference print it, validation
	// refuses a literal outside it naming the set, and the engine refuses a
	// rendered one the same way (ChoiceProblem). For a list, each item is
	// held to it. Leave it empty for a parameter whose values are open,
	// such as a name, a path, or a set the device or a controller defines.
	Choices []string `json:"choices,omitempty"`

	// ChoicesIgnoreCase says the method matches Choices ignoring case and
	// surrounding space, as http.check upper-cases its method and the
	// Windows firewall methods lower-case their words, so validation
	// accepts what the method itself accepts.
	ChoicesIgnoreCase bool `json:"choicesIgnoreCase,omitempty"`

	Description string `json:"description"`

	// Format says where the parameter's text lands when that matters to a
	// rendered value (Phase 117a): on a command line, or as a URL. A
	// runbook that renders into such a parameter is held to a rule for it
	// at validation (internal/validate's template rule). Empty for every
	// other parameter.
	Format ParamFormat `json:"format,omitempty"`

	// AliasOf, when set, names another of the method's parameters this one
	// is a second name for (file.symlink's dest, Ansible's name for its
	// path, so a converted playbook renames nothing). A task giving the alias
	// gives that parameter: validation counts it when the other is
	// required. An alias is never required itself, and names a parameter of
	// its own type that is not an alias (Register refuses anything else).
	AliasOf string `json:"aliasOf,omitempty"`

	// Risk marks a parameter whose setting widens what a task can do
	// beyond its own target: a container's host escapes (privileged, added
	// capabilities, devices, host mounts, the host's network). Empty for
	// every other parameter. The generated reference marks the parameter
	// with it, so whoever reads a method's page before using it sees which
	// settings reach the host; nothing at run time reads it.
	Risk ParamRisk `json:"risk,omitempty"`

	// SecretRefs marks a parameter that may hold secret references,
	// {{ secret.<name>.<field> }} (pkg/secretref), which the method itself
	// resolves from the project's named secrets just before the text reaches
	// the device, and masks by value in everything it returns. The engine
	// never renders one, validation refuses one in any parameter not marked,
	// and a string holding one may hold no other template expression.
	SecretRefs bool `json:"secretRefs,omitempty"`

	// RunSetting marks a parameter a run may set for every task that leaves
	// it out: the Controller's method_settings launch field (a template's
	// default, a saved configuration, a launch, a rollback's request) and the
	// command line's --setting, each naming it <method>.<parameter>. The task's
	// own value always wins, and the parameter's Default applies when neither
	// says. It is for a choice about how a method behaves, never its target:
	// Register allows it only on an optional text parameter with Choices and
	// a Default, no alias, no secret references, not the target and not a
	// call shape's parameter, so a run setting is always one of a closed set
	// of words and never holds a secret (RunSettings).
	//
	// Its Default must be the stricter choice. A Runner built before run
	// settings ignores them and runs the Default, and validation, admission,
	// a Descriptor's CheckCall and DeviceCall all read the task's own
	// parameters before a run's settings fill them, so a run setting may
	// change only how the method itself behaves. A generic dispatcher
	// (svc.*, pkg.*) is filled by its own Doc, not by the concrete method's.
	RunSetting bool `json:"runSetting,omitempty"`
}

// ParamRisk is the kind of reach a parameter's setting adds.
type ParamRisk string

// ParamRiskHostEscape is a setting that lets a container act on its host:
// root on the host, in effect, for whoever controls the container.
const ParamRiskHostEscape ParamRisk = "host_escape"

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
	// ParamFormatKubernetesManifest is one or more Kubernetes objects: a
	// YAML or JSON document stream, one object, or a list of objects.
	// Validation refuses an object whose kind the task's cluster does not
	// serve, by the kinds onboarding discovered there (internal/validate's
	// Kubernetes kind rule).
	ParamFormatKubernetesManifest ParamFormat = "kubernetes_manifest"
	// ParamFormatProjectPath is a path to a file in the project directory
	// that the run writes. An expression rendered into it may read only
	// vars (what the person running the work supplied), never nodes,
	// result or error, which hold what a device returned: a device's output
	// must not choose which project file is replaced.
	ParamFormatProjectPath ParamFormat = "project_path"
	// ParamFormatTemplateFile is a path to a file in the project directory
	// holding a template. The engine reads it, renders it with the run's
	// variables and registered results, and hands the method the rendered
	// text as the parameter named content, with this one removed, so the
	// method never sees a template and never imports a renderer. The path
	// must be written out, not computed; it is read on the command line
	// only, since a Walk-tier Runner holds no project.
	ParamFormatTemplateFile ParamFormat = "template_file"
	// ParamFormatOperand is the name of one thing (a package, a unit, a
	// service, an account or a group) that the method places on a device's
	// command line after the program's own options. A value beginning with
	// "-", which the program would read as an option, or holding "*", "?",
	// "[" or "]", which it would read as a pattern matching many names, is
	// refused: by validation for a literal and by the engine for a rendered
	// value (OperandProblem), and by the method again before anything is sent
	// (pkg/operand). For a list, each item is held to it.
	ParamFormatOperand ParamFormat = "operand"
	// ParamFormatSystemdUnit is a systemd unit, held to ParamFormatOperand's
	// rule except that systemd's two root units, -.mount and -.slice, are
	// admitted by their exact names (operand.SystemdUnit).
	ParamFormatSystemdUnit ParamFormat = "systemd_unit"
	// ParamFormatAptPackage is a Debian package name, held to the characters
	// dpkg gives one, with an optional ":architecture", and refused in every
	// form apt-get reads as something other than that one package: a
	// release, a version, a trailing "-" (remove), a search pattern or a
	// regular expression (operand.AptPackage).
	ParamFormatAptPackage ParamFormat = "apt_package"
	// ParamFormatRPMPackage is an RPM package name, held to the characters
	// rpm gives one and refused in every form dnf reads as something other
	// than one package: a group, a file, a capability, a version constraint
	// or an epoch (operand.RPMPackage).
	ParamFormatRPMPackage ParamFormat = "rpm_package"
	// ParamFormatPackage is a package name a generic method hands to the
	// device's own package manager: one ParamFormatAptPackage or
	// ParamFormatRPMPackage admits, which the concrete method then holds to
	// its own manager's rule (operand.Package).
	ParamFormatPackage ParamFormat = "package"
	// ParamFormatPackageVersion is a version a package manager joins onto a
	// package name (name=version, name-version): no pattern, no leading or
	// trailing "-", and only the characters a Debian or RPM version holds
	// (operand.PackageVersion).
	ParamFormatPackageVersion ParamFormat = "package_version"
	// ParamFormatMountField is one field of a mount command and its fstab
	// line (a mountpoint, a source, a filesystem type, the options): no
	// leading "-" or "#", no space or control character, no "\"
	// (operand.MountField).
	ParamFormatMountField ParamFormat = "mount_field"
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
