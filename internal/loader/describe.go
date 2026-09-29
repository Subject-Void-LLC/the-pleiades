// Package loader: decoding what a program says about itself, and the
// checks every described method must pass before anything registers.
package loader

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// errEmpty is what decodeOne reports for input holding no JSON value at
// all, so a caller can say "printed nothing" rather than "malformed".
var errEmpty = errors.New("no JSON value")

// errTrailing is what decodeOne reports for input carrying anything but
// whitespace after its one JSON value.
var errTrailing = errors.New("data after the JSON value")

// methodNamePattern is the shape a third-party method name must have:
// lowercase segments of letters, digits and underscores, joined by dots,
// at least two of them. It is stricter than collection.Register, which
// only checks for a namespace, because a built-in name is written by this
// repository while this one arrives from a stranger and is then printed
// into logs, errors and reference pages. A newline or an escape sequence
// has no business in any of those.
var methodNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)+$`)

// maxMethodName bounds a method name's length, for the same reason.
const maxMethodName = 200

// decodeOne decodes exactly one JSON value from data into v. Leading and
// trailing whitespace is fine; a second value, or anything else after the
// first, is errTrailing. Unknown fields are accepted on purpose: within
// one protocol version a newer program may send a field this build does
// not know yet, and pkg/external's own contract promises that is safe.
func decodeOne(data []byte, v any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errEmpty
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errTrailing
	}
	return nil
}

// parseDescription decodes a program's describe output and checks its
// shape: the protocol this build speaks, and at least one method. It does
// not look at the methods themselves; validateMethod does.
func parseDescription(data []byte) (external.Description, error) {
	var d external.Description
	if err := decodeOne(data, &d); err != nil {
		switch {
		case errors.Is(err, errEmpty):
			return external.Description{}, errors.New("describe printed nothing")
		case errors.Is(err, errTrailing):
			return external.Description{}, errors.New("describe printed data after its JSON description")
		default:
			return external.Description{}, fmt.Errorf("describe printed invalid JSON: %w", err)
		}
	}
	if d.Protocol != external.ProtocolVersion {
		return external.Description{}, fmt.Errorf("describe reports protocol %d, and this build speaks protocol %d", d.Protocol, external.ProtocolVersion)
	}
	if len(d.Methods) == 0 {
		return external.Description{}, errors.New("describe lists no methods")
	}
	return d, nil
}

// validateMethod checks one described method against everything
// collection.Register will check, plus the rules only a third-party
// method is held to, so that pass two of Load cannot fail partway
// through. running is the build's own version (Options.EngineVersion),
// and reserved the namespaces no external method may use
// (reservedNamespaces).
//
// It reports versionUnchecked, not an error, for an engine version
// constraint a development build could not evaluate (checkEngineVersion).
//
// Register stays the authority. This mirrors its rules because the
// registry offers no way to ask "would you accept this" without also
// registering it, and registering then failing on a later method would
// leave a half-loaded directory with no way to take the first half back.
func validateMethod(m external.DescribedMethod, running string, reserved map[string]bool) (versionUnchecked bool, err error) {
	if len(m.Name) > maxMethodName || !methodNamePattern.MatchString(m.Name) {
		return false, fmt.Errorf("method name %q is not a lowercase, dot-separated name of at least two parts", truncateForMessage(m.Name))
	}
	if err := checkManifestText(reflect.ValueOf(m.Manifest), "manifest"); err != nil {
		return false, fmt.Errorf("method %q: %w", m.Name, err)
	}
	if ns, _, _ := strings.Cut(m.Name, "."); reserved[ns] {
		return false, fmt.Errorf("method %q is in the %q namespace, which belongs to The Pleiades itself; an external Collection must use a namespace of its own, such as your organization's name", m.Name, ns)
	}
	if m.Manifest.Status != collection.StatusImplemented {
		// A program exists to run code, and a declared stub has none. It
		// also has no business claiming a name some real implementation
		// might want.
		return false, fmt.Errorf("method %q has status %q; an external Collection may only provide implemented methods", m.Name, truncateForMessage(string(m.Manifest.Status)))
	}
	if m.Manifest.SupportsCheck && m.Manifest.NoCheckReason != "" {
		return false, fmt.Errorf("method %q declares check support and also a reason it cannot be checked", m.Name)
	}
	// Register's own rule for the declaration (collection.ValidateReversibility),
	// called rather than restated so the two cannot drift. A program's
	// Inverses and ReadOnly are carried but never honored (the engine
	// ignores them for a method with a Provider); a contradictory one is
	// still refused here, as Register would.
	if err := collection.ValidateReversibility(m.Manifest.Reversibility); err != nil {
		return false, fmt.Errorf("method %q %w", m.Name, err)
	}
	for _, p := range m.Manifest.Doc.Params {
		// Register refuses the same thing (checkReservedParams); saying so
		// here keeps a whole directory from loading halfway.
		if collection.IsReservedParam(p.Name) {
			return false, fmt.Errorf("method %q declares a parameter named %q, which the engine reads as the device or tag a task runs on; give it another name", m.Name, p.Name)
		}
	}
	for _, name := range m.Manifest.RequiredCapabilities {
		if _, known := capability.Lookup(name); !known {
			return false, fmt.Errorf("method %q requires capability %q, which this build does not define", m.Name, truncateForMessage(string(name)))
		}
	}
	if _, taken := collection.Lookup(m.Name); taken {
		return false, fmt.Errorf("method %q is already registered by another Collection; an external one never replaces or shadows it", m.Name)
	}
	unchecked, err := checkEngineVersion(m.Manifest.EngineVersion, running)
	if err != nil {
		return false, fmt.Errorf("method %q: %w", m.Name, err)
	}
	return unchecked, nil
}

// The longest an untrusted string may be once quoted into an error: a
// name is short, while stderr is the one place a program explains itself
// and earns more room. Neither may make one error message megabytes long.
const (
	maxNameInMessage   = 120
	maxStderrInMessage = 4096
)

// truncateForMessage shortens an untrusted name before it is quoted into
// an error.
func truncateForMessage(s string) string {
	return clip(s, maxNameInMessage)
}

// stderrForMessage shortens a program's captured stderr before it is
// quoted into an error. The full capture (up to MaxOutput) is still
// logged at debug level.
func stderrForMessage(s string) string {
	return clip(s, maxStderrInMessage)
}

// clip returns s cut to at most limit bytes, marked with "..." when cut,
// and never split partway through a UTF-8 sequence.
func clip(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return strings.ToValidUTF8(s[:limit], "") + "..."
}

// alwaysReserved are namespaces reserved whether or not anything in this
// build registers in them: The Pleiades's own, and Ansible's, whose names a
// migrating playbook already uses and which a third party must not be
// able to impersonate.
var alwaysReserved = []string{"pleiades", "ansible"}

// reservedNamespaces is every namespace an external method may not use:
// each one a method compiled into this binary uses, read from the
// registry when Load runs, so a namespace the catalog adds is reserved
// with no second edit, plus alwaysReserved. A name in any of them can
// then only mean code that ships with The Pleiades.
func reservedNamespaces() map[string]bool {
	reserved := map[string]bool{}
	for _, ns := range collection.BuiltinNamespaces() {
		reserved[ns] = true
	}
	for _, ns := range alwaysReserved {
		reserved[ns] = true
	}
	return reserved
}

// checkManifestText refuses a description whose text could control the
// terminal it is shown on (termsafe.Check): an escape sequence, a
// carriage return, a bidirectional override. Every string in the manifest
// is checked, found by walking it, so a field added to the manifest later
// is covered with no edit here. A description is a third party's text
// shown by `pleiades doc`, the Controller's pages and the approve prompt,
// and none of them has any use for such a character.
func checkManifestText(v reflect.Value, path string) error {
	switch v.Kind() {
	case reflect.String:
		if err := termsafe.Check(v.String()); err != nil {
			return fmt.Errorf("its description's %s holds a %w; a description may not", path, err)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			field := v.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "" {
				name = field.Name
			}
			if err := checkManifestText(v.Field(i), path+"."+name); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if err := checkManifestText(v.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			if err := checkManifestText(iter.Key(), path+" key"); err != nil {
				return err
			}
			if err := checkManifestText(iter.Value(), path+"["+fmt.Sprint(iter.Key().Interface())+"]"); err != nil {
				return err
			}
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			return checkManifestText(v.Elem(), path)
		}
	}
	return nil
}
