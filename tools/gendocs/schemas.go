package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// moduleCatalogEntries builds the full FQCN-to-Manifest map from the live
// pkg/collection registry, in catalogdata's own order, using
// collection.Manifest's own JSON tags directly (its own doc comment
// already calls that encoding "the stable serialized form"): no second,
// hand-maintained shape to keep in sync with it.
func moduleCatalogEntries() (map[string]collection.Manifest, error) {
	entries := make(map[string]collection.Manifest, len(catalogdata.Collections))
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			return nil, fmt.Errorf("gendocs: %q is in catalogdata but never registered into pkg/collection", cfg.Name)
		}
		entries[cfg.Name] = desc.Manifest
	}
	return entries, nil
}

// stringOrList is the JSON Schema shape for engine.StringList: a bare
// scalar, or an array of scalars, matching StringList's own
// UnmarshalYAML (internal/engine/conditional.go).
func stringOrList(description string) map[string]any {
	return map[string]any{
		"description": description,
		"oneOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
}

// taskSchema is the JSON Schema for one Task node (internal/engine/dag.go),
// self-referential through $ref for block/rescue/always. Every property
// key here is checked against engine.ReservedTaskKeys at generation time
// (see checkRunbookSchemaComplete), so this schema cannot silently list a
// key the parser does not accept, or omit one it does.
func taskSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": true, // module-as-key sugar adds one non-reserved key; see internal/engine/task_syntax.go
		"properties": map[string]any{
			"name":          map[string]any{"type": "string", "description": "Free-form label."},
			"fqcn":          map[string]any{"type": "string", "description": "The action this task performs: an engine keyword or a namespaced Collection method."},
			"params":        map[string]any{"type": "object", "description": "Arguments passed to fqcn. Never templated."},
			"register":      map[string]any{"type": "string", "description": "Name to store this task's result under."},
			"check_mode":    checkModeSchema("Run this task (and, on a block, its block, rescue and always tasks) in check mode, even in a real run. Only true; false is refused."),
			"when":          stringOrList("Ansible-compatible conditional, ANDed if a list."),
			"when_or":       stringOrList("Conditional, ORed if a list. No Ansible equivalent."),
			"when_cel":      map[string]any{"type": "string", "description": "One raw CEL expression."},
			"register_mask": stringOrList("Field(s) of this task's own registered result to mask, dotted paths allowed."),
			"secret_mask": map[string]any{
				"type":                 "object",
				"description":          "Retroactively masks fields of an earlier task's registered result.",
				"additionalProperties": false,
				"properties": map[string]any{
					"register": map[string]any{"type": "string"},
					"fields":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []any{"register", "fields"},
			},
			"lock_acquisition": map[string]any{
				"type":        "string",
				"description": "When this task's device locks are acquired.",
				"enum":        []any{"per_device_as_reached", "all_at_plan_time"},
			},
			"block":    map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/task"}},
			"rescue":   map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/task"}},
			"always":   map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/task"}},
			"parallel": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/task"}},
			"tags":     tagsSchema("Names --tags and --skip-tags select this task by. On a block or parallel group they pass down to every task inside."),
		},
	}
}

// tagsSchema is the JSON Schema for engine.TagList: one comma-separated
// string, or a list of strings and numbers. The names all, tagged and
// untagged are refused by the parser, and so by the schema.
func tagsSchema(description string) map[string]any {
	name := map[string]any{"type": "string", "not": map[string]any{"enum": []any{"all", "tagged", "untagged"}}}
	return map[string]any{
		"description": description,
		"oneOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "array", "items": map[string]any{"oneOf": []any{name, map[string]any{"type": "number"}}}},
		},
	}
}

// generateRunbookSchema emits outDir/schemas/runbook.schema.json.
func generateRunbookSchema(outDir string) error {
	if err := checkRunbookSchemaComplete(); err != nil {
		return err
	}

	schema := map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"$id":         "https://pleiades.dev/schemas/runbook.schema.json",
		"title":       "Pleiades runbook",
		"description": "The native Pleiades automation format. Not an Ansible playbook: a top-level YAML list is rejected.",
		"type":        "object",
		"required":    []any{"id", "tasks"},
		// The parser refuses any other top-level key (engine.RunbookKeys).
		"additionalProperties": false,
		"properties": map[string]any{
			"id":         map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]*$", "description": "The runbook's own identifier. Embedded into a NATS subject, so restricted to this character set."},
			"name":       map[string]any{"type": "string", "description": "The runbook's human title."},
			"check_mode": checkModeSchema("Make the whole run a check. Only true; false is refused."),
			"tags":       tagsSchema("Tags every task in the runbook carries, as a play's tags do in Ansible."),
			"hosts":      map[string]any{"type": "string", "description": "Default target for a task that does not set its own."},
			"type":       map[string]any{"type": "string", "enum": []any{"native", "ansible", ""}, "description": "Runbook-type discriminator. \"ansible\" is reserved and non-actionable today."},
			"metadata":   metadataSchema(),
			"pretasks":   map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/task"}},
			"tasks":      map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/task"}},
			"posttasks":  map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/task"}},
		},
		"$defs": map[string]any{
			"task": taskSchema(),
		},
	}

	return writeSchema(outDir, "runbook.schema.json", schema)
}

// metadataSchema is the JSON Schema for engine.Metadata. The parser
// refuses any other key under metadata:, so the schema does too, and
// checkRunbookSchemaComplete holds these properties to Metadata's own
// struct tags.
func metadataSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          "Pleiades-only facts about the runbook as a whole.",
		"additionalProperties": false,
		"properties": map[string]any{
			"service_effecting": map[string]any{"type": "boolean", "description": "Running this can affect live service."},
			"interruptible":     map[string]any{"type": "boolean", "description": "A Runner that loses its Controller may abort this run. Omitted means true."},
			"description":       map[string]any{"type": "string", "description": "A sentence or two about what this runbook does."},
			"category":          map[string]any{"type": "string", "description": "The one catalog bucket this runbook is filed under."},
			"labels":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Free-form catalog filter markers. Not Ansible tags."},
		},
	}
}

// checkModeSchema is the JSON Schema for engine.CheckModeFlag: true, or
// one of Ansible's string spellings of it, in any case.
func checkModeSchema(description string) map[string]any {
	return map[string]any{
		"description": description,
		"oneOf": []any{
			map[string]any{"const": true},
			map[string]any{"type": "string", "pattern": "^([Tt][Rr][Uu][Ee]|[Yy][Ee][Ss]|[Oo][Nn]|[Yy]|[Tt])$"},
		},
	}
}

// checkRunbookSchemaComplete proves taskSchema's own property keys and
// engine.ReservedTaskKeys name exactly the same set, the schema-level
// analogue of taskkeys.go's checkTaskKeysComplete.
func checkRunbookSchemaComplete() error {
	props, _ := taskSchema()["properties"].(map[string]any)

	var undocumented, extra []string
	for key := range engine.ReservedTaskKeys {
		if _, ok := props[key]; !ok {
			undocumented = append(undocumented, key)
		}
	}
	for key := range props {
		if !engine.ReservedTaskKeys[key] {
			extra = append(extra, key)
		}
	}
	if len(undocumented) > 0 {
		return fmt.Errorf("gendocs: runbook schema is missing task key(s) the parser accepts: %v", undocumented)
	}
	if len(extra) > 0 {
		return fmt.Errorf("gendocs: runbook schema has task key(s) the parser does not accept: %v", extra)
	}

	// metadata: the parser refuses any key Metadata's struct tags do not
	// declare, so the schema must list exactly those tags.
	metaProps, _ := metadataSchema()["properties"].(map[string]any)
	var tagged []string
	for f := range reflect.TypeFor[engine.Metadata]().Fields() {
		if name, _, _ := strings.Cut(f.Tag.Get("yaml"), ","); name != "" && name != "-" {
			tagged = append(tagged, name)
		}
	}
	listed := slices.Collect(maps.Keys(metaProps))
	slices.Sort(tagged)
	slices.Sort(listed)
	if !slices.Equal(tagged, listed) {
		return fmt.Errorf("gendocs: runbook schema's metadata properties %v differ from engine.Metadata's keys %v", listed, tagged)
	}
	return nil
}

// generateInventorySchema emits outDir/schemas/inventory.schema.json,
// matching internal/inventory/yaml_plugin.go's real hosts.yaml shape.
func generateInventorySchema(outDir string) error {
	schema := map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"$id":         "https://pleiades.dev/schemas/inventory.schema.json",
		"title":       "Pleiades static inventory",
		"description": "The Crawl-tier inventory.yaml format.",
		"type":        "object",
		"required":    []any{"hosts"},
		"properties": map[string]any{
			"hosts": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":     "object",
					"required": []any{"name", "type"},
					"properties": map[string]any{
						"id":       map[string]any{"type": "string", "description": "Stable device ID, a UUID if written by `pleiades add-host`."},
						"name":     map[string]any{"type": "string"},
						"type":     map[string]any{"type": "string", "description": "A registered device type key, e.g. \"linux_server\"."},
						"classify": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Classification path, provenance only once type is resolved."},
						"tags":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"properties": map[string]any{
							"type":                 "object",
							"additionalProperties": true,
						},
					},
				},
			},
		},
	}
	return writeSchema(outDir, "inventory.schema.json", schema)
}

// generateModuleCatalogSchema emits outDir/schemas/module-catalog.json: not
// a JSON Schema, but the full data dump the schema describes -- every
// registered FQCN's Manifest, straight from the live pkg/collection
// registry. This is the same document `pleiades doc --json` (Wave 4)
// serves, generated here so it exists even before that command is built,
// and identical to it once it is: both read collection.Lookup, nothing
// else.
func generateModuleCatalogSchema(outDir string) error {
	entries, err := moduleCatalogEntries()
	if err != nil {
		return err
	}
	return writeSchema(outDir, "module-catalog.json", entries)
}

// wellKnownDir is where internal/api's own //go:embed picks these same
// files up to serve at /.well-known/pleiades/*.json (internal/api/wellknown.go).
// A schema is written to two places, not one, because go:embed can only
// reach files in or below its own package directory: docs/reference/schemas/
// is where a human (or a docs site) browses it, internal/api/wellknown/ is
// what the running binary actually serves. Both copies come from this one
// function, so they cannot diverge.
const wellKnownDir = "internal/api/wellknown"

func writeSchema(outDir, name string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // a version constraint like ">=1.0.0" should read as itself, not >=1.0.0
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("marshaling %s: %w", name, err)
	}
	data := buf.Bytes()

	for _, dir := range []string{filepath.Join(outDir, "schemas"), wellKnownDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- generated docs, not secret material
			return fmt.Errorf("creating %s: %w", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil { // #nosec G306 -- generated docs, not secret material
			return fmt.Errorf("writing %s/%s: %w", dir, name, err)
		}
	}
	return nil
}
