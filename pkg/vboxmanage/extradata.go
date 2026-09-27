// Extradata: the key and value pairs VirtualBox keeps for a machine (or
// for the host, as "global"), where Pleiades records what VirtualBox
// cannot know about a machine it made, such as the address it gave it.
package vboxmanage

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// The extradata keys a machine Pleiades made records what VirtualBox
// cannot know about it under: the host-only address it was given, and the
// inventory device whose login it was seeded with.
const (
	ExtraAddress = "pleiades/address"
	ExtraDevice  = "pleiades/device"
	// ExtraInstalled marks a machine whose unattended install finished,
	// with the time it did, so an install that stopped part way is not
	// taken for a finished one.
	ExtraInstalled = "pleiades/installed"
)

// extraKeyPattern is an extradata key this package writes or reads.
var extraKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9/._{}-]{0,127}$`)

// checkExtra refuses a key outside extraKeyPattern, and a value holding a
// quote, a control character, or more than 256 characters: each travels
// as one command-line argument.
func checkExtra(key, value string) error {
	if !extraKeyPattern.MatchString(key) {
		return fmt.Errorf("vboxmanage: extradata key %q is not letters, digits and / . _ { } -", key)
	}
	if len(value) > 256 || strings.ContainsAny(value, `"'`) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("vboxmanage: extradata value for %s is longer than 256 characters or holds a quote or control character", key)
	}
	return nil
}

// ExtraData returns vm's extradata, or the host's when vm is "global". A
// machine that is not registered is an error wrapping ErrNotFound.
func (h Host) ExtraData(ctx context.Context, vm string) (map[string]string, error) {
	if vm != "global" {
		if err := CheckName("VM", vm); err != nil {
			return nil, err
		}
	}
	out, err := h.run(ctx, "getextradata", vm, "enumerate")
	if err != nil {
		return nil, err
	}
	data := map[string]string{}
	for _, line := range strings.Split(out.Stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		rest, ok := strings.CutPrefix(line, "Key: ")
		key, value, found := strings.Cut(rest, ", Value: ")
		if !ok || !found {
			return nil, fmt.Errorf("vboxmanage: getextradata gave a line it does not recognize: %q", line)
		}
		data[key] = value
	}
	return data, nil
}

// SetExtraData sets vm's key to value, or deletes the key when value is
// empty. VirtualBox accepts it while the machine runs.
func (h Host) SetExtraData(ctx context.Context, vm, key, value string) error {
	if err := CheckName("VM", vm); err != nil {
		return err
	}
	if err := checkExtra(key, value); err != nil {
		return err
	}
	args := []string{"setextradata", vm, key}
	if value != "" {
		args = append(args, value)
	}
	_, err := h.run(ctx, args...)
	return err
}
