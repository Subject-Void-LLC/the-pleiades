// file.json: one value at a key path in a JSON file, the rest kept.
package file

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/docedit"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnJSON = "file.json"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name:     fqcnJSON,
		Manifest: structuredManifest(jsonDoc()),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return structuredRun(ctx, rc, d, p, fqcnJSON, jsonFormat{}, false)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return structuredRun(ctx, rc, d, p, fqcnJSON, jsonFormat{}, true)
		},
	})
}

// jsonFormat edits JSON through docedit's ordered value.
type jsonFormat struct{}

func (jsonFormat) edit(data []byte, keyPath []docedit.Segment, value any, present bool) ([]byte, string, string, bool, error) {
	if data == nil {
		data = []byte("{}\n")
	}
	doc, format, err := docedit.ParseJSON(data)
	if err != nil {
		return nil, "", "", false, err
	}
	if format.Indent == "" && len(data) == len("{}\n") {
		format.Indent = "  "
	}
	old, had := doc.Get(keyPath)
	before := ""
	if had {
		before = docedit.JSONText(old)
	}
	if !present {
		if !had {
			return data, "", "", false, nil
		}
		if _, err := doc.Delete(keyPath); err != nil {
			return nil, "", "", false, err
		}
		return docedit.EncodeJSON(doc, format), before, "", true, nil
	}
	val, err := docedit.FromAny(value)
	if err != nil {
		return nil, "", "", false, err
	}
	if had && docedit.SameJSON(old, val) {
		return data, before, before, false, nil
	}
	if err := doc.Set(keyPath, val); err != nil {
		return nil, "", "", false, err
	}
	return docedit.EncodeJSON(doc, format), before, docedit.JSONText(val), true, nil
}

func jsonDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Sets, or removes, the value at a key path in a JSON file on the device, keeping the rest of the file.",
		Description: "Reads the file (at most 4 MiB of UTF-8, refused when it holds a duplicate key, which two readers resolve differently) and sets the value at the key path, or removes it with state absent, keeping every other key in its place and every number and string exactly as written, so a 64-bit id or an escaped character survives. The file is written back at the indent unit it used (tabs, or a number of spaces, or on one line), one member per line, so a hand-aligned layout is normalized, and line endings are written as LF. A key already holding the value (by value, however it is spelled) is no change and nothing is written. The write keeps the file's owner, group and mode. The undo writes back the whole earlier text, withheld from the journal; a file the run created is removed. A check makes the same edit, reports the key's value before and after, and writes nothing.",
		Params:      structuredParams("JSON"),
		Returns:     structuredReturns(),
		Examples: []collection.Example{
			{Name: "Move the service to another port", RunbookYAML: "- name: Port\n  file.json:\n    path: /etc/app/config.json\n    key: server.port\n    value: 8443\n"},
			{Name: "Drop a deprecated setting", RunbookYAML: "- name: Remove the old flag\n  file.json:\n    path: /etc/app/config.json\n    key: [features, \"legacy.mode\"]\n    state: absent\n"},
		},
		SeeAlso: []string{"file.yaml", "file.line", "file.copy"},
	}
}
