// Package native: tests of the Runner's comparison of the Collections a
// dispatch names with its own (collections.go), case by case.
package native

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// logLine is one line compareCollections logged.
type logLine struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
	Group string `json:"group"`
	FQCN  string `json:"fqcn"`
}

// compareAndRead runs compareCollections over named and a DAG of fqcns, and
// returns what it logged at debug and above and the warnings it returned
// for the job's log.
func compareAndRead(t *testing.T, named map[string]wire.CollectionIdentity, fqcns ...string) ([]logLine, []string) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	dag := &engine.DAG{Nodes: map[string]*engine.Task{}}
	for i, fqcn := range fqcns {
		dag.Nodes["tasks["+string(rune('0'+i))+"]"] = &engine.Task{FQCN: fqcn}
	}
	warnings := compareCollections(logger, wire.DispatchPayload{JobID: "job-1", DeviceName: "web1", Collections: named}, dag)
	var lines []logLine
	for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(raw) == 0 {
			continue
		}
		var l logLine
		if err := json.Unmarshal(raw, &l); err != nil {
			t.Fatalf("a log line is not JSON: %s", raw)
		}
		lines = append(lines, l)
	}
	return lines, warnings
}

// TestCompareCollections_EachCaseSaysWhatItIs walks the case table: the
// same declarations log nothing (or a debug line when only the version
// differs); other declarations warn, with a distinct line when the versions
// agree; a program's entry, an entry Shape refuses and a group this Runner
// lacks are each warned and not compared; a method this Runner runs from a
// program that the Controller placed in a group gets its own warning; a
// built-in whose group the Controller did not name, beside one it did, is
// logged at info; and a dispatch that names nothing logs nothing. Each
// warning is also returned, once, as a sentence for the job's log, naming
// the group; a debug or info line is not.
func TestCompareCollections_EachCaseSaysWhatItIs(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	reads := collection.Reversibility{ReadOnly: true, Notes: "reads"}
	collection.MustRegister(collection.Descriptor{Name: "file.test_compare", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: reads}})
	collection.MustRegister(collection.Descriptor{Name: "net.test_compare", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: reads}})
	collection.MustRegister(collection.Descriptor{Name: "netbox.sync", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: reads},
		Provider: &collection.Provider{Program: "/opt/collections/netbox", Digest: "sha256:00"}})
	own, err := collection.GroupDigest("file")
	if err != nil {
		t.Fatal(err)
	}
	ownNet, err := collection.GroupDigest("net")
	if err != nil {
		t.Fatal(err)
	}
	other := wire.ManifestDigestPrefix + strings.Repeat("9", 64)
	version := runnerVersion()
	file := func(version, manifests string) map[string]wire.CollectionIdentity {
		return map[string]wire.CollectionIdentity{"file": {Group: "file", Version: version, Manifests: manifests}}
	}

	for _, tc := range []struct {
		name  string
		named map[string]wire.CollectionIdentity
		fqcns []string
		// want is the level and a part of the message of each line logged,
		// in order; none means nothing is logged.
		want [][2]string
		// group is what each returned warning names.
		group string
	}{
		{name: "nothing named", named: nil, fqcns: []string{"file.test_compare"}},
		{name: "the same build", named: file(version, own), fqcns: []string{"file.test_compare"}},
		{name: "another build, the same declarations", named: file("0.3.9", own),
			want: [][2]string{{"DEBUG", "different builds with the same declarations"}}},
		{name: "another build, other declarations", named: file("0.3.9", other), group: "file",
			want: [][2]string{{"WARN", "planned against other declarations"}}},
		{name: "one version, other declarations", named: file(version, other), group: "file",
			want: [][2]string{{"WARN", "stale module catalog or a corrupt build"}}},
		{name: "a program's entry", named: map[string]wire.CollectionIdentity{"file": {Group: "file", Program: true, Manifests: own}}, group: "file",
			want: [][2]string{{"WARN", "external program's identity"}}},
		{name: "an entry Shape refuses", named: map[string]wire.CollectionIdentity{"file": {Group: "file", Manifests: "sha256:00"}}, group: "file",
			want: [][2]string{{"WARN", "no build writes"}}},
		{name: "an entry keyed by another group", named: map[string]wire.CollectionIdentity{"net": {Group: "file", Manifests: own}}, group: "net",
			want: [][2]string{{"WARN", "no build writes"}}},
		// A name no group can take: an FQCN prefix with a character no
		// namespace holds, so the case cannot flip when a group is linked.
		{name: "a group this Runner holds nothing of", named: map[string]wire.CollectionIdentity{"no-such-group": {Group: "no-such-group", Version: version, Manifests: other}}, group: "no-such-group",
			want: [][2]string{{"WARN", "does not include"}}},
		// An entry refused for its shape places nothing: the program's
		// method under it is not warned as one the Controller placed there.
		{name: "a refused entry places no method", named: map[string]wire.CollectionIdentity{"netbox": {Group: "netbox", Manifests: "sha256:00"}},
			fqcns: []string{"netbox.sync"}, group: "netbox",
			want: [][2]string{{"WARN", "no build writes"}}},
		{name: "a method this Runner runs from a program", named: map[string]wire.CollectionIdentity{"netbox": {Group: "netbox", Version: "0.9.0", Manifests: other}},
			fqcns: []string{"netbox.sync"}, group: "netbox",
			want: [][2]string{{"WARN", "does not include"}, {"WARN", "runs it from an external program"}}},
		{name: "a built-in whose group was not named", named: map[string]wire.CollectionIdentity{"net": {Group: "net", Version: version, Manifests: ownNet}},
			fqcns: []string{"file.test_compare", "net.test_compare", "netbox.sync"},
			want:  [][2]string{{"INFO", "named no Collection for this built-in method"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines, warnings := compareAndRead(t, tc.named, tc.fqcns...)
			if len(lines) != len(tc.want) {
				t.Fatalf("logged %+v, want %d line(s) %v", lines, len(tc.want), tc.want)
			}
			warned := 0
			for i, w := range tc.want {
				if lines[i].Level != w[0] || !strings.Contains(lines[i].Msg, w[1]) {
					t.Errorf("line %d is %s %q, want %s with %q", i, lines[i].Level, lines[i].Msg, w[0], w[1])
				}
				if w[0] == "WARN" {
					warned++
				}
			}
			if len(warnings) != warned {
				t.Fatalf("returned %q for the job's log, want one per warning logged (%d)", warnings, warned)
			}
			for _, w := range warnings {
				if !strings.Contains(w, tc.group) {
					t.Errorf("the job's warning %q does not name the group %s", w, tc.group)
				}
			}
		})
	}
}

// TestNamedGroupOf_TakesTheLongestNamedPrefix: a method is placed in the
// accepted group whose name is the longest prefix of it ending at a dot; a
// prefix that does not end at a dot places nothing.
func TestNamedGroupOf_TakesTheLongestNamedPrefix(t *testing.T) {
	accepted := map[string]bool{"net": true, "net.batfish": true}
	for fqcn, want := range map[string]string{
		"net.batfish.check": "net.batfish", "net.ios.save": "net", "netbox.sync": "", "acme.widget": "",
	} {
		got, ok := namedGroupOf(accepted, fqcn)
		if got != want || ok != (want != "") {
			t.Errorf("%s: %q %v, want %q", fqcn, got, ok, want)
		}
	}
}
