// Reading and writing a whole report: a reader refuses another kind and
// a major version it does not know, and ignores a field it does not know;
// a writer never leaves half a document for a reader to find.
package prreport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// ErrNoReport is ReadReport's answer where there is no report.
var ErrNoReport = errors.New("no report")

// ReadReport reads the report at path, refusing another kind and a major
// version this reader does not know; a field it does not know is ignored.
func ReadReport(path string) (Report, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the report this tool was pointed at
	if errors.Is(err, os.ErrNotExist) {
		return Report{}, fmt.Errorf("%w at %s: make pr-check writes one", ErrNoReport, path)
	}
	if err != nil {
		return Report{}, err
	}
	return DecodeReport(data)
}

// DecodeReport reads a report from data as ReadReport does.
func DecodeReport(data []byte) (Report, error) {
	var probe struct {
		Kind   string `json:"kind"`
		Schema int    `json:"schema"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Report{}, fmt.Errorf("reading a report: %w", err)
	}
	if probe.Kind != ReportKind {
		return Report{}, fmt.Errorf("a record of kind %q is not a pr-check report", probe.Kind)
	}
	if probe.Schema != ReportSchema {
		return Report{}, fmt.Errorf("a pr-check report of schema %d, where this reader knows %d: read it with the tools of the commit that wrote it", probe.Schema, ReportSchema)
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return Report{}, fmt.Errorf("reading a report: %w", err)
	}
	return r, nil
}

// WriteJSON writes v to path through a temporary file renamed over it, so a
// reader never sees half a document.
func WriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
