// Package yang: reading net.yang.schema's parameters into a request, every
// refusal made before the device is contacted.
package yang

import (
	"fmt"
	"path"
	"strings"
	"unicode"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// wanted is one module a task names: a name, and a revision when it gives
// one.
type wanted struct {
	name, revision string
}

// key is w as the task wrote it, name or name@revision.
func (w wanted) key() string {
	return yanglib.Schema{Name: w.name, Revision: w.revision}.Key()
}

// request is one task's parameters, read and checked.
type request struct {
	// modules are the modules named, in the order given, once each; none
	// asks for every module the library lists.
	modules []wanted
	// submodules says each module's submodules are fetched beside it.
	submodules bool
	// dest is the project directory written into, cleaned.
	dest string
	// maxModules, maxModuleBytes and maxTotalBytes are the task's bounds.
	maxModules, maxModuleBytes, maxTotalBytes int
}

// readRequest reads params for device, refusing a malformed module name, a
// dest that leaves the project, and a bound below one or past its ceiling.
func readRequest(params map[string]any, device inventory.InventoryItem) (request, error) {
	r := request{}
	var err error
	if r.modules, err = readModules(params[paramModules]); err != nil {
		return request{}, err
	}
	if r.submodules, err = sdk.BoolParamOr(params, paramSubmodules, true); err != nil {
		return request{}, err
	}
	if r.dest, err = readDest(params, device); err != nil {
		return request{}, err
	}
	for _, b := range []struct {
		name         string
		into         *int
		def, ceiling int
	}{
		{paramMaxModules, &r.maxModules, defaultMaxModules, ceilingMaxModules},
		{paramMaxModuleBytes, &r.maxModuleBytes, defaultMaxModuleBytes, ceilingMaxModuleBytes},
		{paramMaxTotalBytes, &r.maxTotalBytes, defaultMaxTotalBytes, ceilingMaxTotalBytes},
	} {
		v, given, err := sdk.IntParam(params, b.name)
		switch {
		case err != nil:
			return request{}, err
		case !given:
			v = b.def
		case v < 1 || v > b.ceiling:
			return request{}, fmt.Errorf("%s is %d, and must be from 1 to %d", b.name, v, b.ceiling)
		}
		*b.into = v
	}
	return r, nil
}

// readModules reads the modules parameter: absent or null for every module,
// else a list of names, each name or name@revision (yanglib.ParseKey), kept
// once each in the order given.
func readModules(v any) ([]wanted, error) {
	if v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		if list, isStrings := v.([]string); isStrings {
			for _, s := range list {
				items = append(items, s)
			}
		} else {
			return nil, fmt.Errorf("%s must be a list of module names, such as [ietf-interfaces, Cisco-IOS-XE-native@2024-07-01]", paramModules)
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%s is an empty list: name the modules to fetch, or leave it out to fetch every module the device lists", paramModules)
	}
	seen := map[wanted]bool{}
	var out []wanted
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s holds %v, which is not a module name", paramModules, item)
		}
		name, revision, err := yanglib.ParseKey(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", paramModules, err)
		}
		if w := (wanted{name, revision}); !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out, nil
}

// readDest reads dest, defaulting to yang/ and the device's name, and
// refuses one that is absolute, climbs out of the project, or holds a
// backslash (a separator on Windows, so never a name's part). The project
// writer holds every file to the rest of the project's rules (never through a
// link, never into .pleiades).
func readDest(params map[string]any, device inventory.InventoryItem) (string, error) {
	dest, err := sdk.OptionalStringParam(params, paramDest)
	if err != nil {
		return "", err
	}
	if dest == "" {
		if device == nil || !segmentOK(device.Name()) {
			return "", fmt.Errorf("the device's name cannot name a directory, so %s has no default here: set %s", paramDest, paramDest)
		}
		dest = "yang/" + device.Name()
	}
	clean := path.Clean(dest)
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(dest, `\`) || strings.Contains(clean, ":") {
		return "", fmt.Errorf("%s %q must be a slash-separated path inside the project, such as yang/core1", paramDest, dest)
	}
	return clean, nil
}

// segmentOK reports whether name can be one directory name: not empty, not
// a dot segment, no separator, drive mark or control character, and at most
// 255 bytes.
func segmentOK(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 255 || strings.ContainsAny(name, `/\:`) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
