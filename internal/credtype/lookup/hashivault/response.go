// Turning one Vault response into one injectable value.
//
// Split from hashivault.go for the ~300 line file convention, not because
// it is a different concern.

package hashivault

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// checkStatus turns an HTTP status into the error an operator can act on.
//
// The three that matter are separated deliberately: "this token cannot read
// that path", "there is nothing at that path" and "the server is unwell"
// send somebody to three different places, and one error for all three
// sends them to the wrong one.
func checkStatus(status int, ref reference) error {
	switch {
	case status == http.StatusOK:
		return nil
	case status == http.StatusForbidden, status == http.StatusUnauthorized:
		return fmt.Errorf("%w: this source credential's token is not permitted to read %s", credtype.ErrLookupReference, ref)
	case status == http.StatusNotFound:
		// Vault answers 404 both for a path that holds nothing and for a
		// mount that does not exist, and it does not distinguish them, so
		// neither does this.
		return fmt.Errorf("%w: nothing is stored at %s, or that mount does not exist", credtype.ErrLookupReference, ref)
	default:
		return fmt.Errorf("%w: the Vault server answered %d reading %s", credtype.ErrLookupReference, status, ref)
	}
}

// valueFrom takes the one field the reference names out of the response.
func (l *Lookup) valueFrom(body []byte, ref reference) (string, error) {
	// Decoded into a shape narrow enough to describe both engine versions,
	// rather than into map[string]any and indexed by hand: a v2 response
	// nests its data one level deeper, and that is the whole difference.
	var payload struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("%w: the response for %s is not the JSON a key/value read returns",
			credtype.ErrLookupReference, ref)
	}

	fields := payload.Data
	if l.kvVersion == "v2" {
		nested, ok := payload.Data["data"]
		if !ok {
			return "", fmt.Errorf("%w: the response for %s carries no data, which a v1 mount read as v2 looks like",
				credtype.ErrLookupReference, ref)
		}
		fields = nil
		if err := json.Unmarshal(nested, &fields); err != nil {
			return "", fmt.Errorf("%w: the response for %s is not the JSON a v2 key/value read returns",
				credtype.ErrLookupReference, ref)
		}
	}

	raw, ok := fields[ref.Key]
	if !ok {
		// Names the key and never the ones that ARE present: a sibling key
		// name is somebody else's secret's name.
		return "", fmt.Errorf("%w: %s holds no key %q", credtype.ErrLookupReference, ref, ref.Key)
	}
	return scalar(raw, ref)
}

// scalar renders one JSON value as the string an injector will use.
//
// A string comes back as itself. A number or a boolean is accepted and
// rendered the way it was written, because an operator who stored a port or
// a flag beside a password should not have to quote it. An object or an
// array is refused: there is no rendering of one that an injector could use
// and no way to guess which part was meant.
func scalar(raw json.RawMessage, ref reference) (string, error) {
	trimmed := strings.TrimSpace(string(raw))

	// Null is checked FIRST, and the ordering is the whole reason this is
	// not three lines. encoding/json unmarshals a JSON null into a string
	// target successfully, leaving it empty and returning no error, so a
	// null reaching the string case below would be indistinguishable from
	// a stored empty string. Both are refused eventually, since an empty
	// external secret is refused upstream, but they are refused with
	// different errors, and "this key holds a null" is the one an operator
	// can act on. Found by a test rather than by reading, which is why it
	// is written down here.
	if trimmed == "null" {
		return "", fmt.Errorf("%w: %s holds a null under %q", credtype.ErrLookupReference, ref, ref.Key)
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	switch {
	case trimmed == "true", trimmed == "false":
		return trimmed, nil
	case strings.HasPrefix(trimmed, "{"), strings.HasPrefix(trimmed, "["):
		return "", fmt.Errorf("%w: %s holds an object or a list under %q, and only a single value can be injected",
			credtype.ErrLookupReference, ref, ref.Key)
	}
	if _, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return trimmed, nil
	}
	return "", fmt.Errorf("%w: %s holds a value under %q this platform cannot render",
		credtype.ErrLookupReference, ref, ref.Key)
}
