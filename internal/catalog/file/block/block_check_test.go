// Package block_test: tests of file.block.set's and file.block.remove's
// checks.
package block_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file/block"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestBlockChecks_PredictWhatARealRunLeaves runs each block method's
// registered check on a recording SSH harness, with the real run from the
// same file as its control: only reads are sent and the file is untouched;
// the change prediction matches the run; the predicted region is exactly
// the one the run's own read-back finds; the stats match; and no undo is
// recorded.
func TestBlockChecks_PredictWhatARealRunLeaves(t *testing.T) {
	const marked = "before\n# BEGIN ANSIBLE MANAGED BLOCK\nold body\n# END ANSIBLE MANAGED BLOCK\nafter\n"
	const plain = "before\nafter\n"
	for _, tc := range []struct {
		name        string
		fqcn        string
		start       string
		extra       map[string]any
		run         collection.Method
		wantChanged bool
	}{
		{"set adds a block", "file.block.set", plain, map[string]any{"block": "new body\n"}, block.Set, true},
		{"set replaces a block", "file.block.set", marked, map[string]any{"block": "new body\n"}, block.Set, true},
		{"set, already there", "file.block.set", marked, map[string]any{"block": "old body"}, block.Set, false},
		{"remove takes a block out", "file.block.remove", marked, nil, block.Remove, true},
		{"remove, no block", "file.block.remove", plain, nil, block.Remove, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := collection.Lookup(tc.fqcn)
			if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
				t.Fatalf("%s does not declare a check", tc.fqcn)
			}
			srv, err := remoteexectest.Start(remoteexectest.Options{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(srv.Close)
			server := blockServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}

			checkPath := blockFile(t, tc.start)
			checkRC := newBlockContext(server)
			checked, err := d.Check(context.Background(), checkRC, newBlockTarget(server), blockParams(checkPath, tc.extra))
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			for _, c := range srv.Commands() {
				if !strings.HasPrefix(c, "if [ -e ") && !strings.HasPrefix(c, "if [ -f ") {
					t.Errorf("the check sent %q, which is not a read", c)
				}
			}
			if got := blockContents(t, checkPath); got != tc.start {
				t.Errorf("the check changed the file to %q", got)
			}
			if checked.Changed != tc.wantChanged {
				t.Errorf("the check predicts changed %v, want %v", checked.Changed, tc.wantChanged)
			}
			if _, recorded := checkRC.stats[sdk.StatInverse]; recorded {
				t.Error("the check recorded an undo instruction")
			}

			runPath := blockFile(t, tc.start)
			runRC := newBlockContext(server)
			ran, err := tc.run(context.Background(), runRC, newBlockTarget(server), blockParams(runPath, tc.extra))
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if ran.Changed != checked.Changed {
				t.Errorf("the run changed %v, the check predicted %v", ran.Changed, checked.Changed)
			}
			if predicted, actual := blockDiffHalf(t, checkRC, "after"), blockDiffHalf(t, runRC, "after"); !reflect.DeepEqual(predicted, actual) {
				t.Errorf("predicted region %v, the run found %v", predicted, actual)
			}
			for key, value := range runRC.stats {
				if key == sdk.StatDiff || key == sdk.StatInverse || key == "path" {
					continue
				}
				if !reflect.DeepEqual(checkRC.stats[key], value) {
					t.Errorf("stat %s: check %v, run %v", key, checkRC.stats[key], value)
				}
			}
		})
	}
}

// TestBlockChecks_FailWhenTheyCannotPredictOrRecord covers a check that
// cannot finish. A set whose block holds a marker line is refused by the
// check exactly as by the real run, before anything is predicted, since
// the marker would then appear twice. A diff or stat that cannot be
// recorded fails the check as it fails the real run. None of them writes.
func TestBlockChecks_FailWhenTheyCannotPredictOrRecord(t *testing.T) {
	const marked = "before\n# BEGIN ANSIBLE MANAGED BLOCK\nold body\n# END ANSIBLE MANAGED BLOCK\nafter\n"
	for _, tc := range []struct {
		name   string
		fqcn   string
		run    collection.Method
		extra  map[string]any
		failOn string
	}{
		{"set, the block holds a marker", "file.block.set", block.Set, map[string]any{"block": "new body\n# END ANSIBLE MANAGED BLOCK\n"}, ""},
		{"set, the diff cannot be recorded", "file.block.set", block.Set, map[string]any{"block": "new body\n"}, sdk.StatDiff},
		{"set, the stats cannot be recorded", "file.block.set", block.Set, map[string]any{"block": "new body\n"}, "present"},
		{"remove, the diff cannot be recorded", "file.block.remove", block.Remove, nil, sdk.StatDiff},
		{"remove, the stats cannot be recorded", "file.block.remove", block.Remove, nil, "present"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := collection.Lookup(tc.fqcn)
			server := startBlockServer(t)
			path := blockFile(t, marked)
			rc := newBlockContext(server)
			rc.failOn = tc.failOn
			if _, err := d.Check(context.Background(), rc, newBlockTarget(server), blockParams(path, tc.extra)); err == nil {
				t.Fatal("the check succeeded")
			}
			if got := blockContents(t, path); got != marked {
				t.Errorf("the check changed the file to %q", got)
			}
			if tc.failOn == "" {
				runRC := newBlockContext(server)
				if _, err := tc.run(context.Background(), runRC, newBlockTarget(server), blockParams(blockFile(t, marked), tc.extra)); err == nil {
					t.Error("the real run accepted what the check refused")
				}
			}
		})
	}
}
