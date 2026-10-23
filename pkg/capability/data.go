// Package capability: capabilities an external Collection program defines
// as data, beside the compiled vocabulary.
package capability

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A compiled capability is two promises: a claim the planner checks, and
// typed accessors a Collection calls (CLIPrompt, SSHHost). An external
// program cannot use the accessors, since its device arrives across a
// process boundary as properties (pkg/external.Device), so for a program a
// capability is a name, the properties its methods read, and a grant some
// one can account for. A data capability is exactly that.
//
// It never weakens the compiled vocabulary:
//
//   - its name is <namespace>.<name>, lower case, so it can never be a
//     compiled name (those end in "Capable" and hold no dot);
//   - it has no Parent, so granting it implies nothing else: a parent would
//     let a grant claim a compiled capability the device's Go type cannot
//     keep;
//   - it may Require compiled capabilities, which a device must have beside
//     it, exactly as a method's own requirements are checked;
//   - its Assert is always true. The promise is not structural; it is the
//     grant record (a program's probe, or an operator's scoped assertion),
//     which only those two write paths may write.
//
// Only internal/loader registers one, for a program it has approved and
// confined, and only a method of that same program may require it.

// DataDefinition is one capability a program defines.
type DataDefinition struct {
	// Name is <namespace>.<name>: the namespace one the program's own
	// methods use, the name snake case.
	Name Name `json:"name"`

	// Provider names the program that defined it.
	Provider string `json:"provider,omitempty"`

	// Doc says, in a sentence, what a device with it can do.
	Doc string `json:"doc,omitempty"`

	// Requires names compiled capabilities a device must also have
	// (SSHTransportCapable for one reached over SSH).
	Requires []Name `json:"requires,omitempty"`

	// Properties are what a grant carries and the program's methods read.
	Properties []DataProperty `json:"properties,omitempty"`

	// Probe names one of the program's own read-only methods whose run on
	// a device decides a grant: pleiades onboard runs it, and a run that
	// sets the stat "granted" to true grants the capability with the
	// properties it sets under "properties". Empty when only an operator's
	// assertion can grant it.
	Probe string `json:"probe,omitempty"`
}

// DataProperty is one property a grant of a data capability carries.
type DataProperty struct {
	// Name is snake case.
	Name string `json:"name"`
	// Type is "string", "int" or "bool".
	Type string `json:"type"`
	// Required says a grant without it is refused.
	Required bool `json:"required,omitempty"`
	// Description says what it holds.
	Description string `json:"description,omitempty"`
}

// The bounds a definition and a grant are held to.
const (
	MaxDataNameLength     = 64
	MaxDataProperties     = 32
	MaxDataRequires       = 8
	MaxDataPropertyLength = 1024
	MaxDataDocLength      = 512
)

var (
	dataName     = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-z][a-z0-9_]*$`)
	propertyName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// IsDataName reports whether name has a data capability's shape, which no
// compiled capability can have.
func IsDataName(name Name) bool {
	return len(name) <= MaxDataNameLength && dataName.MatchString(string(name))
}

// DataNamespace returns the namespace of a data capability's name: the
// part before the dot.
func DataNamespace(name Name) string {
	ns, _, _ := strings.Cut(string(name), ".")
	return ns
}

// ValidateData checks a definition's shape on its own, before anything is
// registered: the name, the bounds, each requirement a known compiled
// capability, and each property's name, type and text.
func ValidateData(def DataDefinition) error {
	if !IsDataName(def.Name) {
		return fmt.Errorf("capability %q: a capability a program defines is named <namespace>.<name>, lower case, at most %d characters", def.Name, MaxDataNameLength)
	}
	if err := plainText(def.Doc, MaxDataDocLength); err != nil {
		return fmt.Errorf("capability %q: doc: %w", def.Name, err)
	}
	if len(def.Requires) > MaxDataRequires {
		return fmt.Errorf("capability %q requires %d capabilities, more than %d", def.Name, len(def.Requires), MaxDataRequires)
	}
	for _, req := range def.Requires {
		d, known := Lookup(req)
		if !known || d.Data != nil {
			return fmt.Errorf("capability %q requires %q, which is not a capability this build compiles in", def.Name, req)
		}
	}
	if len(def.Properties) > MaxDataProperties {
		return fmt.Errorf("capability %q declares %d properties, more than %d", def.Name, len(def.Properties), MaxDataProperties)
	}
	seen := map[string]bool{}
	for _, p := range def.Properties {
		switch {
		case !propertyName.MatchString(p.Name) || len(p.Name) > MaxDataNameLength:
			return fmt.Errorf("capability %q: property %q is not a snake case name of at most %d characters", def.Name, p.Name, MaxDataNameLength)
		case seen[p.Name]:
			return fmt.Errorf("capability %q declares property %q twice", def.Name, p.Name)
		case p.Type != "string" && p.Type != "int" && p.Type != "bool":
			return fmt.Errorf("capability %q: property %q has type %q; a property is a string, an int or a bool", def.Name, p.Name, p.Type)
		}
		if err := plainText(p.Description, MaxDataDocLength); err != nil {
			return fmt.Errorf("capability %q: property %q: %w", def.Name, p.Name, err)
		}
		seen[p.Name] = true
	}
	return nil
}

// RegisterData adds a program's capability to the vocabulary. It refuses,
// rather than panics, a definition ValidateData refuses and a name already
// registered by anyone, compiled or not: a definition comes from a program,
// not from this module's own init.
func RegisterData(def DataDefinition) error {
	if err := ValidateData(def); err != nil {
		return err
	}
	if _, taken := Lookup(def.Name); taken {
		return fmt.Errorf("capability %q is already defined", def.Name)
	}
	stored := def
	stored.Requires = append([]Name(nil), def.Requires...)
	stored.Properties = append([]DataProperty(nil), def.Properties...)
	return vocabulary.Register(string(def.Name), Descriptor{
		Name:   def.Name,
		Data:   &stored,
		Assert: func(any) bool { return true },
	})
}

// DataOf returns the definition of a data capability that is registered.
func DataOf(name Name) (DataDefinition, bool) {
	d, ok := Lookup(name)
	if !ok || d.Data == nil {
		return DataDefinition{}, false
	}
	return *d.Data, true
}

// ValidateGrant checks the properties a grant of def carries: every
// required one present, none undeclared, each of its declared type and
// bounded. A grant read back from JSON holds numbers as float64, which an
// int property accepts only when whole.
func ValidateGrant(def DataDefinition, props map[string]any) error {
	declared := map[string]DataProperty{}
	for _, p := range def.Properties {
		declared[p.Name] = p
		if _, ok := props[p.Name]; p.Required && !ok {
			return fmt.Errorf("a grant of %q needs the property %q", def.Name, p.Name)
		}
	}
	for key, value := range props {
		p, ok := declared[key]
		if !ok {
			return fmt.Errorf("%q declares no property %q", def.Name, key)
		}
		if err := propertyFits(p, value); err != nil {
			return fmt.Errorf("%q property %q: %w", def.Name, key, err)
		}
	}
	return nil
}

// ValidateGrantShape checks the properties of a grant whose definition this
// process does not hold (a Controller loads no program): at most
// MaxDataProperties, each a snake case name holding a bounded plain string,
// a bool or a whole number. The definition is checked where it is held, on
// the command line and on a Runner.
func ValidateGrantShape(props map[string]any) error {
	if len(props) > MaxDataProperties {
		return fmt.Errorf("a grant carries %d properties, more than %d", len(props), MaxDataProperties)
	}
	for key, value := range props {
		if !propertyName.MatchString(key) || len(key) > MaxDataNameLength {
			return fmt.Errorf("property %q is not a snake case name of at most %d characters", key, MaxDataNameLength)
		}
		var err error
		switch value.(type) {
		case string:
			err = propertyFits(DataProperty{Type: "string"}, value)
		case bool:
		case int, int64, float64:
			err = propertyFits(DataProperty{Type: "int"}, value)
		default:
			err = fmt.Errorf("want a string, a bool or a whole number, got %T", value)
		}
		if err != nil {
			return fmt.Errorf("property %q: %w", key, err)
		}
	}
	return nil
}

// propertyFits checks one value against its declared type.
func propertyFits(p DataProperty, value any) error {
	switch p.Type {
	case "string":
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("want a string, got %T", value)
		}
		return plainText(s, MaxDataPropertyLength)
	case "bool":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("want a bool, got %T", value)
		}
	case "int":
		switch v := value.(type) {
		case int, int64:
		case float64:
			if v != float64(int64(v)) {
				return fmt.Errorf("want a whole number, got %v", v)
			}
		default:
			return fmt.Errorf("want an int, got %T", value)
		}
	}
	return nil
}

// plainText refuses text that is too long, is not UTF-8, or holds a
// control character a terminal would act on.
func plainText(s string, max int) error {
	if len(s) > max {
		return fmt.Errorf("longer than %d bytes", max)
	}
	if !utf8.ValidString(s) {
		return fmt.Errorf("not UTF-8")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("holds a control character")
		}
	}
	return nil
}
