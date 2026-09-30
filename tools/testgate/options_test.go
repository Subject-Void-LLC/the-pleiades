// Tests for testgate's options: the arguments each mode passes to go test,
// the combinations it refuses, and how a package list divides into shards.
package main

import (
	"reflect"
	"strings"
	"testing"
)

// TestGoTestArgs pins what each mode asks go test for.
func TestGoTestArgs(t *testing.T) {
	tests := []struct {
		name string
		o    options
		want []string
	}{
		{"the race pass", options{}, []string{"-race", "-timeout", goTestTimeout}},
		{"integration", options{integration: true}, []string{"-race", "-timeout", goTestTimeout, "-tags", "integration", "-count=1"}},
		{"the one pass", options{full: true}, []string{"-race", "-timeout", goTestTimeout, "-tags", "integration", "-cover", "-count=1"}},
		{"with parallelism", options{full: true, parallel: 1}, []string{"-race", "-timeout", goTestTimeout, "-tags", "integration", "-cover", "-count=1", "-p", "1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.o.goTestArgs(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("goTestArgs = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestValidate refuses the combinations with no single meaning.
func TestValidate(t *testing.T) {
	for name, o := range map[string]options{
		"-full and -integration":        {full: true, integration: true},
		"coverage without the one pass": {coverageOut: "c.json"},
		"a shard of ./...":              {shard: "1/2"},
	} {
		if err := o.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := (options{full: true, coverageOut: "c.json", shard: "1/2", packages: []string{"a"}}).validate(); err != nil {
		t.Errorf("a full, sharded, measured run was refused: %v", err)
	}
}

// TestShardOf proves the shards partition the list: every package lands in
// exactly one shard whatever order the list arrived in, and a malformed or
// empty shard is refused rather than run as an empty success.
func TestShardOf(t *testing.T) {
	packages := []string{"e", "a", "d", "c", "b"}
	seen := map[string]int{}
	for _, shard := range []string{"1/3", "2/3", "3/3"} {
		owned, err := shardOf(packages, shard)
		if err != nil {
			t.Fatalf("%s: %v", shard, err)
		}
		for _, p := range owned {
			seen[p]++
		}
	}
	for _, p := range packages {
		if seen[p] != 1 {
			t.Errorf("%s landed in %d shards, want exactly one", p, seen[p])
		}
	}
	if got, _ := shardOf(packages, "1/3"); !reflect.DeepEqual(got, []string{"a", "d"}) {
		t.Errorf("shard 1/3 = %v, want [a d] (sorted, then every third)", got)
	}
	reversed, _ := shardOf([]string{"b", "a"}, "1/2")
	if !reflect.DeepEqual(reversed, []string{"a"}) {
		t.Errorf("the order the list arrived in changed the shard: %v", reversed)
	}
	all, err := shardOf(packages, "")
	if err != nil || len(all) != len(packages) {
		t.Errorf("no shard = %v, %v; want every package", all, err)
	}
	for _, bad := range []string{"0/3", "4/3", "3", "a/b", "1/0"} {
		if _, err := shardOf(packages, bad); err == nil {
			t.Errorf("shard %q was accepted", bad)
		}
	}
	if _, err := shardOf([]string{"a"}, "2/2"); err == nil || !strings.Contains(err.Error(), "owns none") {
		t.Errorf("a shard owning nothing: err = %v, want a refusal", err)
	}
}
