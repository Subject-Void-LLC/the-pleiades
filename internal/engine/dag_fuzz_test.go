package engine_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

func FuzzDAGBuilder(f *testing.F) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	// Old nodes/edges-shaped seeds. These are now invalid input (the
	// hand-wired graph authoring surface is retired), which is exactly
	// why they remain valuable: a fuzz target's job is to never panic on
	// invalid input, and these are a realistic, previously-valid shape
	// that should now fail cleanly rather than panic.
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"nodes": [{"id":"A"}], "edges": [{"from":"A", "to":"A"}]}`))
	f.Add([]byte(`malformed-json`))

	// New tasks-shaped seeds, valid and adversarial, so the fuzzer
	// actually exercises the tree-walk/synthesis path, not just the
	// rejection path for old-shaped input.
	f.Add([]byte(`{"id":"r1","tasks":[{"name":"a","fqcn":"noop"}]}`))
	f.Add([]byte(`{"id":"r2","pretasks":[{"name":"setup","fqcn":"noop"}],"tasks":[{"name":"body","fqcn":"ssh_exec","when":"stat.ok == true"}],"posttasks":[{"name":"teardown","fqcn":"noop"}]}`))
	f.Add([]byte(`{"id":"r3","tasks":[{"name":"grouped","block":[{"name":"b0","fqcn":"noop"},{"name":"b1","fqcn":"noop"}],"rescue":[{"name":"r0","fqcn":"noop"}],"always":[{"name":"a0","fqcn":"noop"}]}]}`))
	// Deeply nested block-in-block-in-block.
	f.Add([]byte(`{"id":"r4","tasks":[{"name":"outer","block":[{"name":"middle","block":[{"name":"inner","block":[{"name":"leaf","fqcn":"noop"}]}]}]}]}`))
	// Adversarial: rescue/always without a block.
	f.Add([]byte(`{"id":"r5","tasks":[{"name":"orphan","fqcn":"noop","rescue":[{"name":"r0","fqcn":"noop"}]}]}`))
	f.Add([]byte(`{"id":"r6","tasks":[{"name":"orphan","fqcn":"noop","always":[{"name":"a0","fqcn":"noop"}]}]}`))
	// Adversarial: neither fqcn nor block, and both fqcn and block.
	f.Add([]byte(`{"id":"r7","tasks":[{"name":"empty"}]}`))
	f.Add([]byte(`{"id":"r8","tasks":[{"name":"both","fqcn":"noop","block":[{"name":"child","fqcn":"noop"}]}]}`))
	// Metadata section present.
	f.Add([]byte(`{"id":"r9","metadata":{"service_effecting":true},"tasks":[{"name":"a","fqcn":"noop"}]}`))

	// Module-as-key sugar (task_syntax.go), JSON path: valid, ambiguous,
	// conflicting, and non-map, mirroring yaml_fuzz_test.go's seeds so the
	// JSON normalizer gets the same adversarial coverage as the YAML one.
	f.Add([]byte(`{"id":"s1","tasks":[{"name":"a","net.cli.command":{"command":"x"}}]}`))
	f.Add([]byte(`{"id":"s2","tasks":[{"name":"a","net.cli.command":{"command":"x"},"net.ios.config":{"lines":[]}}]}`))
	f.Add([]byte(`{"id":"s3","tasks":[{"name":"a","fqcn":"noop","net.cli.command":{"command":"x"}}]}`))
	f.Add([]byte(`{"id":"s4","tasks":[{"name":"a","net.cli.command":"not a map"}]}`))
	f.Add([]byte(`{"id":"s5","tasks":[{"name":"a","net.cli.command":[1,2,3]}]}`))
	f.Add([]byte(`{"id":"s6","tasks":[{"name":"a","noop":null}]}`))

	f.Fuzz(func(t *testing.T, payload []byte) {
		// Just ensure it doesn't panic on arbitrary byte slices
		builder.Build(payload)
	})
}
