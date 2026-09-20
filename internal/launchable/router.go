// This file is the one launch path every consumer uses.
//
// It is the same shape internal/adapters/routing has on the Runner side, for
// the same reason: the decision is a map lookup keyed by a registry key, so
// adding a type changes this package's input rather than any consumer's
// code. Phase 21's adversarial gate is that no consumer branches on which
// sort of launchable it holds, and internal/archtest checks it over the
// parsed source.
//
// One deliberate difference from that package: this Router refuses to be
// built with a gap. A Runner legitimately composes no Ansible adapter, so an
// unroutable kind there is a runtime report rather than a startup failure.
// The Controller always has both launchers it needs, so a missing one is a
// wiring mistake, and the honest moment to say so is at startup rather than
// at 03:00 when a schedule fires.
package launchable

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrNoLauncher is a registered type with nothing composed to launch it. It
// is returned only by NewRouter, since after that the pairing is total.
var ErrNoLauncher = errors.New("launchable: no launcher for this type")

// Request is one launch of one target.
type Request struct {
	Target Target

	// Actor is who or what asked, recorded on whatever this produces: an
	// identity's subject, or the scheduler's own actor string. It is never
	// derived here, because a launcher that invented an actor would be
	// writing an audit trail nobody answers to.
	Actor string

	// SavedConfigID is the saved launch configuration this run uses, or zero
	// for the target's own defaults. A type that takes none refuses a
	// non-zero value rather than ignoring it.
	SavedConfigID int
}

// Launched is what a launch produced.
type Launched struct {
	// RunID identifies the run, in whatever vocabulary its type uses: a
	// job's own job id, or a sync attempt's id as a string. It is opaque
	// here on purpose. A consumer records it so somebody can find the run
	// later, and giving this field a type per launchable type is exactly the
	// branching this package exists to avoid.
	RunID string

	// UnifiedJobType says which sort of run RunID names, so a reader knows
	// which list to look in. Stamped by the Router from the descriptor
	// rather than by each launcher, so a launcher cannot disagree with its
	// own type's declaration.
	UnifiedJobType UnifiedJobType
}

// Launcher runs one launchable type. It is what a composition root supplies
// per type; implementing Preflighter as well is optional.
type Launcher interface {
	Launch(ctx context.Context, req Request) (Launched, error)
}

// Preflighter answers whether a launch would be refused, without launching.
//
// It exists so a refusal lands at the write rather than at the first fire.
// A template bound to a credential that prompts at launch can never run
// unattended, and a manual project has nothing to fetch; both are things
// somebody should be told while they are still looking at the form.
//
// It is a separate interface from Launcher because the two can be
// implemented by one type and need not be: a launcher with nothing to check
// in advance is honest to omit it, and the Router treats an absent
// Preflighter as "nothing to object to".
type Preflighter interface {
	Preflight(ctx context.Context, req Request) error
}

// Router launches a target through the launcher its type declares.
type Router struct {
	byType map[string]Launcher
}

// NewRouter pairs every registered type with its launcher, refusing any gap
// in either direction.
//
// A registered type with no launcher would fail only when something tried to
// launch it, which for a schedule is unattended and hours later. A launcher
// for an unregistered type is the mirror mistake: the code is composed, the
// type is invisible to every picker and validator, and nothing says why
// (FAILURE_PATTERNS.md #52 is this project's own record of shipping exactly
// that shape).
func NewRouter(launchers map[string]Launcher) (*Router, error) {
	byType := make(map[string]Launcher, len(launchers))
	for _, d := range Types() {
		l, ok := launchers[d.Type]
		if !ok || l == nil {
			return nil, fmt.Errorf("%w: %q is registered but nothing was composed to launch it", ErrNoLauncher, d.Type)
		}
		byType[d.Type] = l
	}
	for key := range launchers {
		if _, ok := Lookup(key); !ok {
			return nil, fmt.Errorf("%w: a launcher was composed for %q, which is not a registered launchable type", ErrUnknownType, key)
		}
	}
	return &Router{byType: byType}, nil
}

// Launch runs one target and reports what it started.
//
// The routing is a map lookup. Everything before it is the checking that
// belongs to every type alike: an actor is required, and a saved
// configuration is refused for a type that takes none, re-checked here even
// though a write path checked it earlier, because this is the last point
// before something actually runs.
func (r *Router) Launch(ctx context.Context, req Request) (Launched, error) {
	d, launcher, err := r.resolve(req)
	if err != nil {
		return Launched{}, err
	}
	if strings.TrimSpace(req.Actor) == "" {
		return Launched{}, fmt.Errorf("launchable: launching %s named no actor", req.Target.Name)
	}
	if err := d.AdmitsSavedConfig(req.SavedConfigID); err != nil {
		return Launched{}, err
	}

	launched, err := launcher.Launch(ctx, req)
	if err != nil {
		return Launched{}, err
	}

	// The run's type comes from the descriptor rather than from whatever the
	// launcher filled in, so the two cannot disagree about what was started.
	launched.UnifiedJobType = d.UnifiedJobType
	return launched, nil
}

// Preflight reports whether this launch would be refused, without starting
// anything. A type whose launcher implements no Preflighter has nothing to
// object to in advance, which is not an error.
func (r *Router) Preflight(ctx context.Context, req Request) error {
	d, launcher, err := r.resolve(req)
	if err != nil {
		return err
	}
	if err := d.AdmitsSavedConfig(req.SavedConfigID); err != nil {
		return err
	}
	if p, ok := launcher.(Preflighter); ok {
		return p.Preflight(ctx, req)
	}
	return nil
}

// resolve finds a request's descriptor and launcher together, since every
// caller here needs both and neither is useful alone.
func (r *Router) resolve(req Request) (Descriptor, Launcher, error) {
	d, err := Describe(req.Target)
	if err != nil {
		return Descriptor{}, nil, err
	}
	launcher, ok := r.byType[d.Type]
	if !ok {
		return Descriptor{}, nil, fmt.Errorf("%w: %q (this controller can launch %s)", ErrNoLauncher, d.Type, r.describe())
	}
	return d, launcher, nil
}

// Types lists what this Router can launch, in a stable order.
func (r *Router) Types() []string {
	out := make([]string, 0, len(r.byType))
	for t := range r.byType {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// describe renders the routable types for an error message.
func (r *Router) describe() string {
	types := r.Types()
	if len(types) == 0 {
		return "nothing"
	}
	return strings.Join(types, ", ")
}
