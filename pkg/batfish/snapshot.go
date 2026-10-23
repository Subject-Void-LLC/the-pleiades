// Package batfish: building a snapshot archive from configurations.
package batfish

import (
	"archive/zip"
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"time"
)

// configName is what a configuration's file name may be: a node name,
// never a path.
var configName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// Snapshot returns a snapshot archive holding each configuration, by node
// name, as snapshot/configs/<name>.cfg: the layout Batfish reads a
// directory of device configurations from. Entries are written in name
// order with a fixed time, so the same configurations make the same bytes.
func Snapshot(configs map[string]string) ([]byte, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("batfish: a snapshot needs at least one configuration")
	}
	names := make([]string, 0, len(configs))
	for name := range configs {
		if !configName.MatchString(name) {
			return nil, fmt.Errorf("batfish: %q is not a node name a snapshot file can carry", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, name := range names {
		f, err := w.CreateHeader(&zip.FileHeader{Name: "snapshot/configs/" + name + ".cfg", Method: zip.Deflate, Modified: stamp})
		if err != nil {
			return nil, err
		}
		if _, err := f.Write([]byte(configs[name])); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
