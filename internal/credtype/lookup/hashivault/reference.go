// The binding metadata, and how it reaches Resolve without a parser.
//
// # Why this is JSON rather than a compact string
//
// credtype.Lookup.Resolve takes ONE string, and a binding's addressing is
// four fields: the mount, the path inside it, the key inside that, and
// optionally a version. Something has to flatten the four into the one.
//
// The obvious encodings all need a parser with an ambiguity in it. A Vault
// path legitimately contains slashes, so a slash cannot separate the path
// from the key. A key can contain almost any byte, so no single separator
// character is safe against a key that contains it, and a version appended
// after a marker is ambiguous against a key that ends in that marker
// followed by digits. Each of those is a small hand-rolled parser guarding
// a rule somebody has to remember, which is the shape pkg/rfc2217's own
// three bounds defects came out of and which Phase 100's Blocker 3 cites as
// the general argument.
//
// So the flattening is encoding/json, which round-trips exactly, has no
// ambiguity to get wrong, and fails as a decode error rather than as a
// quietly different path. The cost is a reference that is not pleasant to
// read, and that cost is not paid where it would matter: every error in
// this package renders the reference through String below, which prints
// mount, path and key in the form an operator recognises.
//
// This is safe only because a row-backed source's Resolve is never handed a
// reference from anywhere except Reference. The string form
// "<source>:<reference>" cannot reach this type, because it registers as a
// factory by namespace and never as a deployment-wide Lookup by name.

package hashivault

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// defaultMount is the mount a binding reads when it names none.
//
// "secret" is the mount a default Vault installation creates, so a binding
// that omits it addresses the common case rather than failing on it.
const defaultMount = "secret"

// reference is one binding's addressing, as it travels to Resolve.
//
// The JSON tags are AWX's own metadata field names, minus their
// secret_/_ prefixes, so the mapping from an AWX CredentialInputSource row
// is a rename rather than a translation.
type reference struct {
	Mount   string `json:"mount"`
	Path    string `json:"path"`
	Key     string `json:"key"`
	Version int    `json:"version,omitempty"`
}

// String renders a reference the way an operator wrote it.
//
// This is what every error in this package prints, which is why the JSON
// encoding above costs nothing in readability. It carries no secret: a
// mount, a path and a key are pointers to a value rather than the value.
func (r reference) String() string {
	s := r.Mount + "/" + r.Path + "#" + r.Key
	if r.Version > 0 {
		s += " (version " + strconv.Itoa(r.Version) + ")"
	}
	return s
}

// Reference turns one binding's metadata into the string Resolve reads.
//
// The metadata keys are AWX's own, so an AWX CredentialInputSource row
// carries over field for field: secret_backend, secret_path, secret_key and
// secret_version.
func (Factory) Reference(metadata map[string]string) (string, error) {
	ref := reference{
		Mount: strings.Trim(strings.TrimSpace(metadata["secret_backend"]), "/"),
		Path:  strings.Trim(strings.TrimSpace(metadata["secret_path"]), "/"),
		Key:   metadata["secret_key"],
	}
	if ref.Mount == "" {
		ref.Mount = defaultMount
	}
	if ref.Path == "" {
		return "", fmt.Errorf("%w: this binding sets no secret_path, so there is nothing to read", credtype.ErrLookupReference)
	}
	if ref.Key == "" {
		return "", fmt.Errorf("%w: this binding sets no secret_key, and a key/value read returns a whole document rather than one value",
			credtype.ErrLookupReference)
	}

	if raw := strings.TrimSpace(metadata["secret_version"]); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return "", fmt.Errorf("%w: secret_version %q is not a positive whole number", credtype.ErrLookupReference, raw)
		}
		if v > maxSecretVersion {
			return "", fmt.Errorf("%w: secret_version %d is larger than any real Vault version", credtype.ErrLookupReference, v)
		}
		ref.Version = v
	}

	if err := checkSegments(ref); err != nil {
		return "", err
	}

	encoded, err := json.Marshal(ref)
	if err != nil {
		// Unreachable: every field is a string or an int. Reported rather
		// than ignored, because a silently empty reference would read as a
		// binding nobody filled in.
		return "", fmt.Errorf("%w: this binding could not be encoded", credtype.ErrLookupReference)
	}
	return string(encoded), nil
}

// decodeReference reads back what Reference wrote.
func decodeReference(raw string) (reference, error) {
	var ref reference
	decoder := json.NewDecoder(strings.NewReader(raw))
	// A reference this package did not write is a wiring error rather than
	// data to be tolerant of, so an unknown field is refused instead of
	// dropped.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ref); err != nil {
		return reference{}, fmt.Errorf("%w: this binding's metadata is not one this source wrote", credtype.ErrLookupReference)
	}
	if err := checkSegments(ref); err != nil {
		return reference{}, err
	}
	return ref, nil
}

// checkSegments refuses anything that would let a mount or a path address
// something other than the secret it names.
//
// The mount and the path become URL path segments. url.URL.JoinPath escapes
// each segment, so a slash inside one cannot introduce a new one, but it
// also CLEANS the result, which means a ".." segment would resolve upward
// into a different Vault API endpoint rather than failing. Refusing dot
// segments outright means there is nothing left for cleaning to resolve,
// which is the same argument internal/credtype/lookup/file makes for
// refusing every separator rather than cleaning and then checking.
func checkSegments(ref reference) error {
	for _, part := range []struct {
		name  string
		value string
	}{{"secret_backend", ref.Mount}, {"secret_path", ref.Path}, {"secret_key", ref.Key}} {
		if part.value == "" {
			return fmt.Errorf("%w: %s is empty", credtype.ErrLookupReference, part.name)
		}
		if strings.ContainsRune(part.value, 0) {
			return fmt.Errorf("%w: %s contains a null byte", credtype.ErrLookupReference, part.name)
		}
	}
	if strings.ContainsRune(ref.Mount, '/') {
		return fmt.Errorf("%w: secret_backend %q contains a slash, and a mount is one path element",
			credtype.ErrLookupReference, ref.Mount)
	}
	for _, segment := range append(strings.Split(ref.Path, "/"), ref.Mount) {
		if segment == "" {
			return fmt.Errorf("%w: secret_path %q has an empty element", credtype.ErrLookupReference, ref.Path)
		}
		if segment == "." || segment == ".." {
			return fmt.Errorf("%w: %q contains a %q element, which would address something other than the secret it names",
				credtype.ErrLookupReference, ref.Path, segment)
		}
	}
	return nil
}
