package render

// The bounds this renderer enforces, and why each number is what it is.
//
// internal/engine/cel.go sets the precedent for stating a limit's reasoning
// beside the constant rather than in a commit message, because the number
// on its own reads as arbitrary and the next person to raise it needs to
// know what it was protecting.
const (
	// maxSourceBytes bounds one template's source text. An injector
	// template is a handful of characters in every real credential type in
	// the parity corpus; the largest plausible one is a multi-line
	// certificate wrapper, which is still comfortably under a kilobyte.
	// 64 KiB is roughly two orders of magnitude of headroom over anything
	// observed, and it exists so a single API write cannot hand the parser
	// a megabyte of nested brace openings.
	maxSourceBytes = 64 << 10

	// maxOutputBytes bounds one rendered result. The largest legitimate
	// output is a generated credential file, which for a PEM bundle or a
	// kubeconfig runs to a few tens of kilobytes. 1 MiB bounds the
	// amplification a filter chain can produce from a small input without
	// constraining any real value.
	maxOutputBytes = 1 << 20

	// maxPathDepth bounds a dotted reference such as a.b.c.d. The deepest
	// path this grammar needs is the reserved filename namespace at
	// tower.filename.cert, which is three. 16 leaves room for a caller
	// with a genuinely nested extra-vars structure and still refuses a
	// path built to exhaust the parser's own stack.
	maxPathDepth = 16

	// maxFilterChain bounds how many filters one expression may chain.
	// Real injector templates use zero or one. Eight is generous and
	// bounds the work a single expression can queue up.
	maxFilterChain = 8

	// maxCachedTemplates bounds the compile cache.
	//
	// This is a real difference from internal/engine/cel.go's unbounded
	// program cache, and the difference is the source of the templates
	// rather than a change of opinion. A CEL expression comes from a
	// committed runbook, so the set of distinct expressions a process ever
	// sees is bounded by the repository. A credential-type injector
	// template comes from an API-writable database row, so the set is
	// bounded only by what a holder of credential:write chooses to create.
	// An unbounded cache there is a memory-growth vector under ordinary
	// authorization rather than under compromise.
	//
	// On overflow this engine compiles without caching rather than
	// evicting. That keeps the cache free of eviction bookkeeping, keeps
	// a compiled Template immutable and shareable, and makes the
	// degradation a slowdown rather than a failure. An operator who
	// notices the slowdown has thousands of distinct credential types,
	// which is a fact worth learning.
	maxCachedTemplates = 4096
)
