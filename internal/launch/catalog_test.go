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
