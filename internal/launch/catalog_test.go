package launch_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
)

// This file covers the catalog port: the enumerable set of launchable
// definitions, and the create-time existence check built on it.
//
// The check exists because of where a missing definition used to surface.
// A template naming nothing was created with a 201, launched with a 202
// and a job id, and first failed at fan-out, as a failed job in the audit
// trail, discovered later and possibly by somebody else. AWX's escape
// hatch at least errors at template save or job start; this one errored
// three stages after the mistake was made.

// kindCatalogOver builds a KindCatalog over a fixed id list.
func kindCatalogOver(ids ...string) launch.KindCatalog {
	return launch.KindCatalogFuncs{
		ListFunc: func(context.Context) ([]string, error) { return ids, nil },
		VerifyFunc: func(_ context.Context, definition string) error {
			for _, id := range ids {
				if id == definition {
					return nil
				}
			}
			return fmt.Errorf("%w: %q", launch.ErrDefinitionNotFound, definition)
		},
	}
}

func TestSourceCatalog_ListsKindsInRegistryOrderAndSkipsUnwiredOnes(t *testing.T) {
	catalog := launch.NewSourceCatalog(map[string]launch.KindCatalog{
		"runbook":  kindCatalogOver("patch", "upgrade"),
		"playbook": kindCatalogOver("site"),
	})

	entries, err := catalog.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// Registry order is sorted by kind, so playbook precedes runbook, and
	// a picker built from this reads grouped and stable.
	var got []string
	for _, e := range entries {
		got = append(got, e.Kind+":"+e.Definition)
	}
	want := "playbook:site runbook:patch runbook:upgrade"
	if strings.Join(got, " ") != want {
		t.Errorf("List = %v, want %q", got, want)
	}

	// A deployment with no playbook source lists runbooks alone rather
	// than failing: absent is a configuration, not an outage.
	runbooksOnly := launch.NewSourceCatalog(map[string]launch.KindCatalog{
		"runbook": kindCatalogOver("patch"),
	})
	entries, err = runbooksOnly.List(context.Background())
	if err != nil || len(entries) != 1 {
		t.Errorf("List over one wired kind = %v, %v; want the one runbook", entries, err)
	}
}

func TestSourceCatalog_AnOutageIsNotAnEmptyCatalog(t *testing.T) {
	catalog := launch.NewSourceCatalog(map[string]launch.KindCatalog{
		"runbook": kindCatalogOver("patch"),
		"playbook": launch.KindCatalogFuncs{
			ListFunc:   func(context.Context) ([]string, error) { return nil, errors.New("disk fell off") },
			VerifyFunc: func(context.Context, string) error { return nil },
		},
	})

	// The whole listing fails rather than narrowing: a picker missing
	// every playbook looks exactly like a deployment that has none, and a
	// control built over a partial answer misleads.
	if _, err := catalog.List(context.Background()); err == nil {
		t.Fatal("List over a failing source succeeded, silently narrowing the catalog")
	}
}

func TestSourceCatalog_VerifyRefusesAKindWithNoSourceByName(t *testing.T) {
	catalog := launch.NewSourceCatalog(map[string]launch.KindCatalog{
		"runbook": kindCatalogOver("patch"),
	})

	err := catalog.Verify(context.Background(), "playbook", "site")
	if !errors.Is(err, launch.ErrDefinitionNotFound) {
		t.Fatalf("Verify for an unwired kind = %v, want ErrDefinitionNotFound", err)
	}
	// The message carries the difference between "no such playbook" and
	// "this deployment cannot have playbooks", because only one of the
	// two is fixed by checking the spelling.
	if !strings.Contains(err.Error(), "no source") {
		t.Errorf("Verify error = %q, which does not say the deployment has no source for the kind", err)
	}
}

func TestStaticCatalog_VerifyIsMembership(t *testing.T) {
	catalog := launch.StaticCatalog(
		launch.CatalogEntry{Kind: "runbook", Definition: "patch"},
	)
	if err := catalog.Verify(context.Background(), "runbook", "patch"); err != nil {
		t.Errorf("Verify(listed) = %v, want nil", err)
	}
	// The same definition under the other kind is refused: an entry is a
	// pair, not an id.
	if err := catalog.Verify(context.Background(), "playbook", "patch"); !errors.Is(err, launch.ErrDefinitionNotFound) {
		t.Errorf("Verify(same id, other kind) = %v, want ErrDefinitionNotFound", err)
	}
}

// TestSourceCatalog_VerifyAcceptsWhatItsSourceResolves is the port's own
// happy path, and until now nothing exercised it.
//
// Every existing test here reaches Verify either before a source is
// consulted (the unwired-kind refusal) or through the static catalog's
// membership check, so KindCatalogFuncs.VerifyFunc had never once been
// called. That matters more for this port than for most: its whole
// purpose is to answer "yes, this is launchable here" at template create,
// and an aggregate that could only ever answer "no" would pass every
// assertion this file already makes.
func TestSourceCatalog_VerifyAcceptsWhatItsSourceResolves(t *testing.T) {
	catalog := launch.NewSourceCatalog(map[string]launch.KindCatalog{
		"runbook": kindCatalogOver("patch", "upgrade"),
	})

	if err := catalog.Verify(context.Background(), "runbook", "patch"); err != nil {
		t.Errorf("Verify(a definition the source resolves) = %v, want nil", err)
	}
	// And the refusal still comes from the source rather than from the
	// aggregate, so the two answers are genuinely distinguishable.
	err := catalog.Verify(context.Background(), "runbook", "never-written")
	if !errors.Is(err, launch.ErrDefinitionNotFound) {
		t.Fatalf("Verify(unknown definition) = %v, want ErrDefinitionNotFound", err)
	}
	if strings.Contains(err.Error(), "no source") {
		t.Errorf("Verify error = %q, which is the unwired-kind message: a wired source's own refusal must not be reported as a missing source", err)
	}
}

// TestSourceCatalog_VerifyReportsASourceOutageAsItself proves the
// distinction KindCatalog's own doc comment insists on: a source failure
// is an outage, not a refusal, and must not reach a caller as
// ErrDefinitionNotFound.
//
// Confusing the two would tell an operator their runbook does not exist
// while the real answer is that the thing holding runbooks is down, which
// is the one wrong answer that sends somebody looking in the wrong place.
func TestSourceCatalog_VerifyReportsASourceOutageAsItself(t *testing.T) {
	outage := errors.New("the runbook source is unreachable")
	catalog := launch.NewSourceCatalog(map[string]launch.KindCatalog{
		"runbook": launch.KindCatalogFuncs{
			ListFunc:   func(context.Context) ([]string, error) { return nil, outage },
			VerifyFunc: func(context.Context, string) error { return outage },
		},
	})

	err := catalog.Verify(context.Background(), "runbook", "patch")
	if !errors.Is(err, outage) {
		t.Fatalf("Verify during an outage = %v, want the source's own error", err)
	}
	if errors.Is(err, launch.ErrDefinitionNotFound) {
		t.Error("a source outage was reported as ErrDefinitionNotFound, which tells an operator their definition does not exist when the truth is that nothing could look")
	}
}

// TestStaticCatalog_ListReturnsItsOwnCopy proves the listing half of the
// static catalog, and that a caller mutating the slice it gets back
// cannot reach into the catalog itself.
//
// The copy is the assertion worth making: StaticCatalog is documented as
// the composition-in-tests and smallest-deployment path, so its entries
// are wired once at startup and read many times, and a caller that sorted
// or filtered the returned slice in place would quietly reshape what
// every later reader sees.
func TestStaticCatalog_ListReturnsItsOwnCopy(t *testing.T) {
	catalog := launch.StaticCatalog(
		launch.CatalogEntry{Kind: "runbook", Definition: "patch"},
		launch.CatalogEntry{Kind: "playbook", Definition: "site"},
	)

	entries, err := catalog.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(entries))
	}

	entries[0] = launch.CatalogEntry{Kind: "runbook", Definition: "overwritten"}

	again, err := catalog.List(context.Background())
	if err != nil {
		t.Fatalf("second List: %v", err)
	}
	if again[0].Definition != "patch" {
		t.Errorf("mutating a returned entry changed the catalog: second List[0] = %q, want %q", again[0].Definition, "patch")
	}
}

// TestStaticCatalog_ListIsEmptyRatherThanNilSafe proves an empty static
// catalog lists nothing without failing, which is the deployment that
// wires no sources at all.
func TestStaticCatalog_ListIsEmptyRatherThanNilSafe(t *testing.T) {
	entries, err := launch.StaticCatalog().List(context.Background())
	if err != nil {
		t.Fatalf("List on an empty catalog: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List on an empty catalog returned %d entries, want 0", len(entries))
	}
}
