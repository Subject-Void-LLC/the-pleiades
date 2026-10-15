// file.yaml: one value at a key path in a YAML file, comments and the rest
// kept.
package file

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/docedit"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnYAML = "file.yaml"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name:     fqcnYAML,
		Manifest: structuredManifest(yamlDoc()),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return structuredRun(ctx, rc, d, p, fqcnYAML, yamlFormat{}, false)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return structuredRun(ctx, rc, d, p, fqcnYAML, yamlFormat{}, true)
		},
	})
}

// yamlFormat edits YAML through docedit's nodes.
type yamlFormat struct{}

func (yamlFormat) edit(data []byte, keyPath []docedit.Segment, value any, present bool) ([]byte, string, string, bool, error) {
	doc, err := docedit.ParseYAML(data)
	if err != nil {
		return nil, "", "", false, err
	}
	old, had, err := doc.Get(keyPath)
	if err != nil {
		return nil, "", "", false, err
	}
	before := ""
	if had {
		before = docedit.YAMLText(old)
	}
	if !present {
		if !had {
			return data, "", "", false, nil
		}
		if _, err := doc.Delete(keyPath); err != nil {
			return nil, "", "", false, err
		}
		out, err := doc.Encode()
		return out, before, "", err == nil, err
	}
	val, err := docedit.ValueNode(value)
	if err != nil {
		return nil, "", "", false, err
	}
	after := docedit.YAMLText(val)
	if had && before == after {
		return data, before, after, false, nil
	}
	if err := doc.Set(keyPath, val); err != nil {
		return nil, "", "", false, err
	}
	out, err := doc.Encode()
	return out, before, after, err == nil, err
}

func yamlDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Sets, or removes, the value at a key path in a YAML file on the device, keeping its comments and the rest of the file.",
		Description: "Reads the file (at most 4 MiB of UTF-8, one document; refused when it holds a duplicate key) and sets the value at the key path, or removes it with state absent, keeping comments and key order, and a replaced value's own comments. The rest of the document is written back in go.yaml.in/yaml/v3's style at the indent the file used, so quoting and spacing may be normalized; it is not a byte-for-byte edit. A path through an alias is refused, since one edit would change every place the alias is used. A key already holding the value is no change and nothing is written. The write keeps the file's owner, group and mode. The undo writes back the whole earlier text, withheld from the journal; a file the run created is removed. A check makes the same edit, reports the key's value before and after, and writes nothing.",
		Params:      structuredParams("YAML"),
		Returns:     structuredReturns(),
		Examples: []collection.Example{
			{Name: "Raise the replica count", RunbookYAML: "- name: Replicas\n  file.yaml:\n    path: /srv/app/values.yaml\n    key: web.replicas\n    value: 3\n"},
		},
		SeeAlso: []string{"file.json", "file.line"},
	}
}
