// Package runbook: DirSource, the filesystem-backed Source implementation
// that resolves a runbook id to a YAML file under a fixed root directory
// (internal/inventory.DefaultRunbookDir names the same "runbooks/"
// convention cmd/pleiades's scaffolded projects already use, though this
// package accepts any directory a caller points it at, not just that
// default). id is caller-controlled, sourced from an HTTP query parameter
// by the Controller-side dispatch path this package exists for, so every
// step between "id" and "bytes read off disk" is a real trust boundary;
// see validRunbookID and Get's own comments for how each step is closed.
package runbook

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

// validRunbookID matches a runbook id that is safe to use in a filesystem
// path: 1 to 64 characters, letters, digits, hyphens, and underscores
// only. Applied BEFORE any path construction (Get's first real statement),
// so no caller-controlled byte ever reaches filepath.Join. This is the
// primary defense of this package's own Schema/Injection Hardening
// boundary: Phase 14 (The Dispatcher)'s own checklist item of that name
// notes the dispatcher takes a runbook id straight from an HTTP query
// parameter, with no runbook storage, and therefore no path-construction
// boundary, behind it at all until this package existed. An id like
// "../../etc/passwd" or one carrying a NUL byte is rejected here, before
// Get ever calls filepath.Join, not caught after the fact by inspecting
// the joined result (see the belt-and-suspenders re-check further down
// Get for why that second check exists too, and why it should never
// actually fire given this regex).
var validRunbookID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// cacheEntry is one Flyweight-cached compiled Runbook, paired with the
// source file's os.FileInfo.ModTime() recorded at compile time. A later
// Get compares this against the file's current mtime to decide whether
// the cached Runbook is still current; see dirSource.Get. This mirrors
// internal/engine/cel.go's own Flyweight cache of compiled CEL programs:
// the real compile work (here, engine.Builder.BuildFromYAML plus the
// capability walk) happens once per distinct, unchanged file rather than
// once per Get call.
type cacheEntry struct {
	runbook *Runbook
	modTime time.Time
}

// dirSource is the filesystem-backed Source implementation. See
// NewDirSource.
type dirSource struct {
	// dir is the runbook root, always an absolute, filepath.Clean-ed path
	// (computed once in NewDirSource), so every per-call traversal check
	// in Get compares against a stable, already-normalized prefix rather
	// than re-deriving it from a possibly-relative dir argument on every
	// single call.
	dir string

	// builder compiles a runbook YAML payload into a *engine.DAG. It is
	// built once, in NewDirSource, from a real engine.NewCELEvaluator, so
	// that a runbook using when/when_or/when_cel conditionals compiles
	// exactly the way cmd/pleiades/load.go's own production path compiles
	// one (AGENTS.md RULE 0: a test, and by extension this production
	// code path, only counts as representative if it exercises the same
	// real config path the platform actually runs). A bare zero-value
	// engine.Builder{} carries a nil Evaluator, which panics the moment a
	// real runbook's conditional task tries to compile
	// (Conditional.Compile calls into it unconditionally whenever
	// when/when_or/when_cel is set); reusing one real Builder across
	// every Get call also means every runbook this dirSource ever
	// compiles shares one CEL Evaluator's own Flyweight program cache,
	// the identical benefit this file's own mtime-keyed Runbook cache
	// gives at the whole-runbook level.
	builder *engine.Builder

	// mu guards cache. A plain sync.Mutex, not sync.RWMutex: Get's hot
	// path (a cache hit) is a single map read plus an os.Stat, cheap
	// enough that a full mutex costs nothing extra, and it is the same
	// primitive internal/engine/cel.go's own Flyweight cache already uses
	// for the identical shape of problem, so this package needs no new
	// shared cache primitive of its own.
	mu    sync.Mutex
	cache map[string]cacheEntry
}

// NewDirSource builds a Source backed by the YAML runbook files directly
// under dir (one file per runbook, named "<id>.yaml"; see Get). It fails
// closed at construction time, the same shape cmd/controller/main.go's own
// loadEnvelopeService already uses for MASTER_ENCRYPTION_KEY: a
// misconfigured or unreadable runbook directory is a startup-time fatal
// error the operator must fix, never a condition Get discovers lazily on a
// caller's first request, which would surface as an opaque per-request 500
// with no clear cause instead of a clear, immediate failure the moment the
// process starts.
func NewDirSource(dir string) (Source, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("runbook directory is not accessible: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("runbook directory path %q is not a directory", dir)
	}
	// os.Stat succeeding only proves dir exists; it says nothing about
	// whether this process can actually list its contents (e.g. the
	// directory's execute bit is cleared for this user). Attempting a
	// real read here, once, at startup, is what "and is readable" in this
	// function's own doc comment requires; a permission failure here
	// surfaces exactly like every other fail-closed startup check in
	// cmd/controller/main.go instead of on some later request.
	if _, err := os.ReadDir(dir); err != nil {
		return nil, fmt.Errorf("runbook directory is not readable: %w", err)
	}

	// dir is normalized to an absolute, cleaned path once here so every
	// later Get call's traversal check (below) compares against a stable
	// prefix, rather than re-resolving a possibly-relative dir argument
	// (e.g. ".") on every single call.
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve runbook directory to an absolute path: %w", err)
	}

	// A real CEL evaluator, not a nil one: see dirSource.builder's own
	// doc comment for why a zero-value Builder cannot stand in here.
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		return nil, fmt.Errorf("failed to init CEL evaluator for runbook compilation: %w", err)
	}

	return &dirSource{
		dir:     absDir,
		builder: engine.NewBuilder(eval),
		cache:   make(map[string]cacheEntry),
	}, nil
}

// Get resolves id to its compiled Runbook. See Source.Get for the general
// contract; this implementation additionally, in order:
//
//  1. Validates id against validRunbookID before any path construction.
//  2. Builds the candidate file path and defensively re-verifies it is
//     still lexically inside dir.
//  3. Treats a missing file as ErrNotFound, wrapped with id, never the
//     filesystem path.
//  4. Compiles the file with a real engine.Builder.
//  5. Computes Required as capability_rule.go's own lookup, deduplicated
//     and order-stable (requiredCapabilities, below).
//  6. Caches the compiled *Runbook, keyed by id, invalidated on mtime
//     change (Flyweight; see cacheEntry).
func (d *dirSource) Get(ctx context.Context, id string) (*Runbook, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Step 1: reject before any path is built. id is caller-controlled
	// (an HTTP query parameter, per this package's own doc comment), so
	// this check runs first, deliberately before id ever reaches
	// filepath.Join, and the rejection message below names neither id nor
	// any path: id has not been proven safe to display at this point, and
	// a rejected id never will be.
	if !validRunbookID.MatchString(id) {
		return nil, fmt.Errorf("invalid runbook id: must match %s", validRunbookID.String())
	}

	// Step 2: build the candidate path, then defensively re-verify it is
	// still lexically inside dir. Given validRunbookID above already
	// excludes "/", "\", and ".", this branch should be structurally
	// unreachable; it is kept anyway as belt-and-suspenders, in the same
	// spirit as internal/lock/nats.go's own itemIDValid: a cheap, total
	// check on this package's own real injection boundary costs nothing,
	// and it does not depend on validRunbookID never changing in some way
	// that reopens this gap in the future.
	joined := filepath.Join(d.dir, id+".yaml")
	cleaned := filepath.Clean(joined)
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve runbook path: %w", err)
	}
	if !strings.HasPrefix(absPath, d.dir+string(filepath.Separator)) {
		return nil, fmt.Errorf("resolved runbook path escapes the runbook directory")
	}

	// mtime must be known before deciding whether a cached entry is still
	// valid, so this Stat happens unconditionally, whether or not a
	// cached entry exists yet for id.
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Step 3: id is safe to echo from here on. It already passed
			// validRunbookID, and this is the ErrNotFound branch, not the
			// invalid-id one above, so a caller can tell the two apart via
			// errors.Is(err, ErrNotFound): true here, false for a
			// validation rejection.
			return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to check runbook %q: %w", id, err)
	}
	modTime := info.ModTime()

	d.mu.Lock()
	if entry, ok := d.cache[id]; ok && entry.modTime.Equal(modTime) {
		d.mu.Unlock()
		return entry.runbook, nil
	}
	d.mu.Unlock()

	// #nosec G304 -- absPath is built from an id already validated against
	// validRunbookID (letters, digits, hyphens, underscores only, 1-64
	// characters) and re-verified above to resolve lexically inside d.dir;
	// this is the same "validated before use" trust boundary
	// BuildFromYAMLFile's own #nosec comment documents for a CLI-supplied
	// path, except here the validation is this package's own allow-list
	// rather than the operating system shell's argument handling.
	payload, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			// The file existed at the Stat above and is gone by the time
			// Read runs (a real, if narrow, TOCTOU window): still a
			// not-found from this caller's point of view, reported the
			// identical way as the Stat-time miss above.
			return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to read runbook %q: %w", id, err)
	}

	dag, err := d.builder.BuildFromYAML(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to compile runbook %q: %w", id, err)
	}

	rb := &Runbook{ID: id, Required: requiredCapabilities(dag)}

	d.mu.Lock()
	d.cache[id] = cacheEntry{runbook: rb, modTime: modTime}
	d.mu.Unlock()

	return rb, nil
}

// requiredCapabilities computes Runbook.Required from a compiled DAG:
// engine.ActionCapability[task.FQCN] for every node in dag.Nodes,
// deduplicated, mirroring internal/validate/capability_rule.go's own
// "for id, task := range world.DAG.Nodes { required, ok :=
// engine.ActionCapability[task.FQCN] ... }" loop exactly, so this package
// and plan-time validation can never disagree about what a given runbook
// requires.
//
// dag.Nodes is a map, so Go's own iteration order over it is randomized;
// node IDs are sorted first so the returned slice's order depends only on
// the compiled runbook's own content, not on map iteration order, giving
// Runbook.Required's own "order-stable" guarantee (see its doc comment in
// runbook.go).
func requiredCapabilities(dag *engine.DAG) []capability.Name {
	ids := make([]string, 0, len(dag.Nodes))
	for id := range dag.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	seen := make(map[capability.Name]bool, len(ids))
	var required []capability.Name
	for _, id := range ids {
		task := dag.Nodes[id]
		name, ok := engine.ActionCapability[task.FQCN]
		if !ok {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		required = append(required, name)
	}
	return required
}
