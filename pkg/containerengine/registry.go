// Registry accounts for pulls and pushes: which registry an image names, and
// the X-Registry-Auth value for it.
package containerengine

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/distribution/reference"
	"github.com/moby/moby/api/types/registry"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// ValidImage reports whether image is a reference a caller may put into an
// engine's command line or request: one the engines' own grammar accepts
// (github.com/distribution/reference, which Docker and Podman both parse
// with). That grammar admits no whitespace, quote or shell metacharacter,
// and a reference that begins with "-" is refused by name first, since a
// command line reads one as an option rather than as the image.
func ValidImage(image string) error {
	if image == "" {
		return fmt.Errorf("containerengine: the image reference is empty")
	}
	if image[0] == '-' {
		return fmt.Errorf("containerengine: image reference %q begins with \"-\", which a command line reads as an option", image)
	}
	if _, err := reference.ParseNormalizedNamed(image); err != nil {
		return fmt.Errorf("containerengine: %q is not an image reference: %w", image, err)
	}
	return nil
}

// RegistryHostOf returns the registry an image reference names, normalized
// as the engine normalizes it: "alpine" and "library/alpine" are Docker
// Hub's ("docker.io"), "ghcr.io/org/app:1" is "ghcr.io", and
// "localhost:5000/app" is "localhost:5000".
func RegistryHostOf(image string) (string, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return "", fmt.Errorf("containerengine: image reference %q: %w", image, err)
	}
	return wire.RegistryHost(reference.Domain(named)), nil
}

// RegistryAuth returns the X-Registry-Auth value for image's registry,
// built from the account rc holds for that registry's host and no other,
// or "" when rc holds none: the pull or push then goes anonymous, and a
// private registry's own refusal says what was missing. The value is sent
// for the one request and never written to the device.
func RegistryAuth(rc sdk.RunbookContext, image string) (string, error) {
	host, err := RegistryHostOf(image)
	if err != nil {
		return "", err
	}
	creds, ok := rc.(sdk.RegistryCredentials)
	if !ok {
		return "", nil
	}
	user, password, ok := creds.RegistryCredential(host)
	if !ok {
		return "", nil
	}
	value, err := EncodeRegistryAuth(user, password, host)
	if err != nil {
		return "", err
	}
	// The header carries the password encoded, which a masking set holding
	// the password alone would not find; the run masks the encoded value too.
	sdk.RegisterSecret(rc, value)
	return value, nil
}

// EncodeRegistryAuth is the X-Registry-Auth value for one account on host, as
// RegistryAuth builds it: the Runner masks this same value from a child's
// output, since it knows the account it sent and not the header.
func EncodeRegistryAuth(user, password, host string) (string, error) {
	return encodeAuth(registry.AuthConfig{Username: user, Password: password, ServerAddress: host})
}

// encodeAuth encodes an account as the Engine API's X-Registry-Auth header
// value: base64url of its JSON.
func encodeAuth(auth registry.AuthConfig) (string, error) {
	data, err := json.Marshal(auth)
	if err != nil {
		return "", fmt.Errorf("containerengine: encoding a registry credential: %w", err)
	}
	return base64.URLEncoding.EncodeToString(data), nil
}
