// Ansible's group_vars and host_vars directories for a static inventory,
// folded at load time into each device's vars property.
//
// Beside the inventory file (hosts.yaml) a project may keep
//
//	group_vars/<group>.yml   (or .yaml, or a directory of such files)
//	host_vars/<host>.yml     (or .yaml, or a directory of such files)
//
// A group is a tag: this inventory has no other grouping, so a host belongs
// to one group per tag it carries, and group_vars/all applies to every host.
// A device's variables are layered, widest first, and the last to name a
// variable wins (a key is replaced whole, never merged into):
//
//  1. group_vars/all;
//  2. group_vars/<tag> for each of the host's tags, in alphabetical order of
//     the tag name, so a tag later in the alphabet wins over an earlier one
//     (a stable rule, where Ansible's depends on its group graph);
//  3. host_vars/<host name>, so the device's own file beats every group;
//  4. the host's own properties.vars in hosts.yaml, the most specific.
//
// A directory is read recursively in lexical file order, later files
// winning. Only .yml and .yaml files are read. Two forms for one name (a
// .yml and a .yaml, or a file and a directory) are refused as ambiguous.
//
// Nothing is dropped silently: a key that is not a variable name, a key
// named like a secret, a text value shaped like a secret, a file that is
// not a mapping, or one over a size limit is an error naming the file.
package inventory

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"go.yaml.in/yaml/v3"
)

// Directory names and the group every host belongs to.
const (
	groupVarsDir = "group_vars"
	hostVarsDir  = "host_vars"
	allGroup     = "all"

	// maxVarsFileBytes bounds one vars file, so a mistaken path cannot load
	// an unbounded file into every device's properties.
	maxVarsFileBytes = 1 << 20
)

// varName is the shape of a variable name; internal/engine's CheckVarNames
// accepts exactly this (a test holds the two equal, since this package
// cannot import the engine).
var varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// CheckVarName reports whether name can be a variable name, as
// {{ vars.name }} reads it.
func CheckVarName(name string) error {
	if !varName.MatchString(name) {
		return fmt.Errorf("%q is not a variable name: use letters, digits and underscores, not starting with a digit", name)
	}
	return nil
}

// checkDeviceVars refuses vars, read from source, when a key is not a
// variable name or when a key or any text inside a value is shaped like a
// secret.
func checkDeviceVars(source string, vars map[string]any) error {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := CheckVarName(k); err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
		if redact.SecretName(k) {
			return fmt.Errorf("%s: variable %q is named like a secret; keep secrets in the credential store, not in inventory variables", source, k)
		}
		if hasSecretText(vars[k]) {
			return fmt.Errorf("%s: the value of variable %q is shaped like a secret; keep secrets in the credential store, not in inventory variables", source, k)
		}
	}
	return nil
}

// hasSecretText reports whether v, or any string nested in it, is shaped like
// a secret.
func hasSecretText(v any) bool {
	switch x := v.(type) {
	case string:
		return redact.SecretShaped(x)
	case map[string]any:
		for _, e := range x {
			if hasSecretText(e) {
				return true
			}
		}
	case []any:
		for _, e := range x {
			if hasSecretText(e) {
				return true
			}
		}
	}
	return false
}

// foldDeviceVars returns the vars property h should carry: the layers the
// package documents, read from the directories beside dir's inventory file,
// with h's own properties.vars on top. It returns nil when no layer exists.
func foldDeviceVars(dir string, h HostSpec) (map[string]any, error) {
	var layers []map[string]any

	// Groups: "all" first, then each tag once, alphabetically.
	groups := []string{allGroup}
	seen := map[string]bool{allGroup: true}
	tags := append([]string(nil), h.Tags...)
	sort.Strings(tags)
	for _, t := range tags {
		if !seen[t] {
			seen[t] = true
			groups = append(groups, t)
		}
	}
	for _, g := range groups {
		layer, err := readVarsSource(filepath.Join(dir, groupVarsDir), g)
		if err != nil {
			return nil, err
		}
		layers = append(layers, layer)
	}
	host, err := readVarsSource(filepath.Join(dir, hostVarsDir), h.Name)
	if err != nil {
		return nil, err
	}
	layers = append(layers, host)

	if raw, present := h.Properties[inventory.VarsProperty]; present {
		own, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("host %s: property %s must be a mapping of variable names to values, not %T", h.Name, inventory.VarsProperty, raw)
		}
		if err := checkDeviceVars(fmt.Sprintf("host %s, properties.%s in %s", h.Name, inventory.VarsProperty, DefaultInventoryFilename), own); err != nil {
			return nil, err
		}
		layers = append(layers, own)
	}

	var out map[string]any
	for _, layer := range layers {
		for k, v := range layer {
			if out == nil {
				out = map[string]any{}
			}
			out[k] = v
		}
	}
	return out, nil
}

// withFoldedVars returns h's properties with the vars property replaced by
// the folded one. The properties map is copied, never changed in place.
func (r *fileRepository) withFoldedVars(h HostSpec) (map[string]any, error) {
	folded, err := foldDeviceVars(filepath.Dir(r.hostsPath), h)
	if err != nil {
		return nil, fmt.Errorf("host %s: %w", h.Name, err)
	}
	if folded == nil {
		return h.Properties, nil
	}
	props := make(map[string]any, len(h.Properties)+1)
	for k, v := range h.Properties {
		props[k] = v
	}
	props[inventory.VarsProperty] = folded
	return props, nil
}

// unfoldVars undoes withFoldedVars for a write. props is the properties an
// item holds and onDisk the host's row as hosts.yaml has it. When the item's
// vars are exactly the folded ones, the files' content must not be copied
// into hosts.yaml, so the row's own properties.vars (or none) is written back
// instead; vars an operator changed by other means are written as they are.
func (r *fileRepository) unfoldVars(props map[string]any, onDisk HostSpec) map[string]any {
	have, present := props[inventory.VarsProperty]
	if !present {
		return props
	}
	folded, err := foldDeviceVars(filepath.Dir(r.hostsPath), onDisk)
	if err != nil || folded == nil || !reflect.DeepEqual(have, folded) {
		return props
	}
	if own, ok := onDisk.Properties[inventory.VarsProperty]; ok {
		props[inventory.VarsProperty] = own
	} else {
		delete(props, inventory.VarsProperty)
	}
	return props
}

// readVarsSource reads base/<name> in its one allowed form and returns its
// variables, or nil when none exists. A name that cannot be a single path
// element (a tag such as "a/b") has no file by construction.
func readVarsSource(base, name string) (map[string]any, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return nil, nil
	}
	var found []string
	for _, cand := range []string{name + ".yml", name + ".yaml", name} {
		p := filepath.Join(base, cand)
		if _, err := os.Stat(p); err == nil {
			found = append(found, p)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("cannot read %s: %w", p, err)
		}
	}
	switch len(found) {
	case 0:
		return nil, nil
	case 1:
	default:
		return nil, fmt.Errorf("%s names variables twice: keep one of %s", name, strings.Join(found, ", "))
	}
	info, err := os.Stat(found[0])
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", found[0], err)
	}
	if !info.IsDir() {
		// A bare file with no extension is not a vars file.
		if !hasVarsExt(found[0]) {
			return nil, nil
		}
		return readVarsFile(found[0])
	}
	out := map[string]any{}
	walkErr := filepath.WalkDir(found[0], func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !hasVarsExt(p) {
			return nil
		}
		layer, err := readVarsFile(p)
		if err != nil {
			return err
		}
		for k, v := range layer {
			out[k] = v
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// hasVarsExt reports whether path is a .yml or .yaml file.
func hasVarsExt(path string) bool {
	ext := filepath.Ext(path)
	return ext == ".yml" || ext == ".yaml"
}

// readVarsFile reads one vars file: a mapping (or an empty file), within the
// size limit, holding only acceptable variables.
func readVarsFile(path string) (map[string]any, error) {
	f, err := os.Open(path) // #nosec G304 -- a path under the project's own group_vars or host_vars directory, not untrusted input
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxVarsFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if len(data) > maxVarsFileBytes {
		return nil, fmt.Errorf("%s is over %d bytes; split it or move the data out of inventory variables", path, maxVarsFileBytes)
	}
	var vars map[string]any
	if err := yaml.Unmarshal(data, &vars); err != nil {
		return nil, fmt.Errorf("%s is not a mapping of variable names to values: %w", path, err)
	}
	if err := checkDeviceVars(path, vars); err != nil {
		return nil, err
	}
	return vars, nil
}
