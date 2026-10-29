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
	// amplification a filter chain or a for loop can produce from a small
	// input without constraining any real value. It is checked after every
	// write, inside loops too, so a loop stops at the bound rather than
	// building its whole output first.
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

	// maxLoopIterations bounds the for loop iterations one render may run,
	// counted across every loop in the template, nested ones included, so
	// two nested loops over 99 items each spend 9,900 between them (99 of
	// the outer loop, 9,801 of the inner).
	//
	// A loop is the only construct that repeats work, and its list comes
	// from data (a register, an inventory, a launch variable), so without a
	// count a template over a large list, or a loop nested in another over
	// the same list, does work the template's own size says nothing about:
	// two nested loops over the 100,000-item list a register can hold are
	// ten billion iterations, most of them writing nothing the output bound
	// would catch. 10,000 covers a file rendered from a few thousand items,
	// such as a hosts file or an access list for a large site, or 99 by 99
	// nested, and with maxEvaluations keeps the worst case a render can be
	// made to run small (TestStatementBounds). A template that needs more is generating data in a template
	// and should build it in a task instead.
	maxLoopIterations = 10000

	// maxEvaluations bounds the steps one render may take: each piece of
	// text written, each expression evaluated, each condition tested and
	// each set. maxLoopIterations alone does not bound the work, since one
	// iteration's body can be most of a 64 KiB template, a few thousand
	// statements that write nothing. 1 Mi steps is 10,000 iterations of a
	// body of about a hundred pieces.
	maxEvaluations = 1 << 20

	// maxNestingDepth bounds how deep if and for statements may nest. Real
	// templates nest two or three deep. 16 matches maxPathDepth and keeps
	// the recursive evaluator's stack shallow whatever the source holds.
	maxNestingDepth = 16

	// maxExprDepth bounds how deep one expression may nest: parentheses,
	// list and mapping literals, filter and test arguments, and nots. Real
	// expressions nest two or three deep. 32 keeps the recursive parser and
	// evaluator shallow whatever the source holds.
	maxExprDepth = 32

	// maxLiteralItems bounds one list or mapping literal, and one argument
	// list. A literal in a template is a handful of choices to test with
	// in; 256 is far past that, and a longer list belongs in vars.
	maxLiteralItems = 256

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
