package engine_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

// FuzzBuildFromYAML ensures malformed, adversarial, or deeply nested YAML
// can never panic the runbook parser. It may legitimately return an error,
// but it must never crash the process that would otherwise be validating
// or running a user's runbook.
func FuzzBuildFromYAML(f *testing.F) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	// Old nodes/edges-shaped seeds. The hand-wired graph authoring surface
	// is retired, so these are now invalid input, which is exactly why
	// they remain valuable: a fuzz target's job is to never panic on
	// invalid input, and these are a realistic, previously-valid shape
	// that should now fail cleanly rather than panic.
	f.Add([]byte("id: runbook-1\nnodes:\n  - id: A\n    action: START\nedges: []\n"))
	f.Add([]byte("id: cycle\nnodes:\n  - id: A\n  - id: B\nedges:\n  - from: A\n    to: B\n  - from: B\n    to: A\n"))
	f.Add([]byte(""))
	f.Add([]byte("nodes: *anchor_that_does_not_exist"))
	f.Add([]byte("id: &a [*a]")) // self-referential anchor

	// A curated "billion laughs" alias bomb (Phase 39, Schema & Injection
	// Hardening): nested anchors each aliasing the previous level nine
	// times, expanding exponentially by the last level. Named regression
	// coverage for this exact shape lives in
	// TestBuildFromYAML_RejectsAliasBomb (yaml_test.go); it is seeded here
	// too so the fuzzer explores mutations around it.
	f.Add([]byte("id: bomb\ntasks:\n  - name: a\n    fqcn: noop\n    params:\n      a: &a [1,1,1,1,1,1,1,1,1]\n      b: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a]\n      c: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b]\n      d: [*c,*c,*c,*c,*c,*c,*c,*c,*c]\n"))

	// Top-level YAML lists, the shape of a real Ansible playbook, must be
	// rejected cleanly by the pre-parse sniff, never panic it.
	f.Add([]byte("- hosts: all\n  tasks: []\n"))
	f.Add([]byte("- - - nested\n- [1, 2, 3]\n- {a: b}\n"))
	f.Add([]byte("[]"))

	// Arbitrary "type" values must be rejected or accepted through the
	// buildFromDef switch, never panic it.
	f.Add([]byte("id: t1\ntype: native\nnodes: []\nedges: []\n"))
	f.Add([]byte("id: t2\ntype: ansible\nnodes: []\nedges: []\n"))
	f.Add([]byte("id: t3\ntype: something-else\nnodes: []\nedges: []\n"))
	f.Add([]byte("type:\n  nested: map\n"))
	f.Add([]byte("type: [a, list, not, a, string]"))

	// Deeply nested structures must not blow the sniff's stack or panic.
	f.Add([]byte("a: {b: {c: {d: {e: {f: [1, 2, {g: h}]}}}}}\n"))

	// New tasks-shaped seeds, valid and adversarial, so the fuzzer
	// actually exercises the new pretasks/tasks/posttasks/block/rescue/
	// always parsing and tree-walk/synthesis path, not just the rejection
	// path for old-shaped input.
	f.Add([]byte("id: r1\ntasks:\n  - name: a\n    fqcn: noop\n"))
	f.Add([]byte("id: r2\npretasks:\n  - name: setup\n    fqcn: noop\ntasks:\n  - name: body\n    fqcn: ssh_exec\n    when: \"stat.ok == true\"\nposttasks:\n  - name: teardown\n    fqcn: noop\n"))
	f.Add([]byte("id: r3\nmetadata:\n  service_effecting: true\ntasks:\n  - name: a\n    fqcn: noop\n"))
	f.Add([]byte("id: r4\ntasks:\n  - name: grouped\n    block:\n      - name: b0\n        fqcn: noop\n      - name: b1\n        fqcn: noop\n    rescue:\n      - name: r0\n        fqcn: noop\n    always:\n      - name: a0\n        fqcn: noop\n"))
	// Deeply nested block-in-block-in-block.
	f.Add([]byte("id: r5\ntasks:\n  - name: outer\n    block:\n      - name: middle\n        block:\n          - name: inner\n            block:\n              - name: leaf\n                fqcn: noop\n"))
	// Adversarial: rescue/always without a block.
	f.Add([]byte("id: r6\ntasks:\n  - name: orphan\n    fqcn: noop\n    rescue:\n      - name: r0\n        fqcn: noop\n"))
	f.Add([]byte("id: r7\ntasks:\n  - name: orphan\n    fqcn: noop\n    always:\n      - name: a0\n        fqcn: noop\n"))
	// Adversarial: neither fqcn nor block, and both fqcn and block.
	f.Add([]byte("id: r8\ntasks:\n  - name: empty\n"))
	f.Add([]byte("id: r9\ntasks:\n  - name: both\n    fqcn: noop\n    block:\n      - name: child\n        fqcn: noop\n"))

	f.Fuzz(func(t *testing.T, payload []byte) {
		_, _ = builder.BuildFromYAML(payload)
	})
}
