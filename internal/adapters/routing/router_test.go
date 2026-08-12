package routing_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/routing"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// recordingAdapter remembers what it was asked to run. A double, and the
// right kind: the behaviour under test is which adapter a payload reaches,
// which cannot be observed with real adapters without a real broker, real
// SSH and real Docker for an assertion about a map lookup.
type recordingAdapter struct {
	name string
	got  []wire.DispatchPayload
}

func (a *recordingAdapter) Execute(_ context.Context, payload wire.DispatchPayload) error {
	a.got = append(a.got, payload)
	return nil
}

func newRouter() (*routing.Router, *recordingAdapter, *recordingAdapter) {
	native := &recordingAdapter{name: "native"}
	legacy := &recordingAdapter{name: "legacy"}
	return routing.New(map[string]routing.Executor{
		"native": native,
		"legacy": legacy,
	}), native, legacy
}

func TestRouter_SendsEachKindToTheAdapterItsDescriptorDeclares(t *testing.T) {
	router, native, legacy := newRouter()
	ctx := context.Background()

	if err := router.Execute(ctx, wire.DispatchPayload{JobID: "j1", Kind: "runbook"}); err != nil {
		t.Fatalf("Execute(runbook): %v", err)
	}
	if err := router.Execute(ctx, wire.DispatchPayload{JobID: "j2", Kind: "playbook"}); err != nil {
		t.Fatalf("Execute(playbook): %v", err)
	}

	// The badge is not decoration: the two kinds really do reach two
	// different executors, which is what makes "runbook or playbook" a
	// statement about what will run rather than a label on a page.
	if len(native.got) != 1 || native.got[0].JobID != "j1" {
		t.Errorf("the native adapter received %+v, want the runbook dispatch", native.got)
	}
	if len(legacy.got) != 1 || legacy.got[0].JobID != "j2" {
		t.Errorf("the legacy adapter received %+v, want the playbook dispatch", legacy.got)
	}
}

func TestRouter_AnAbsentKindReachesTheAdapterItAlwaysDid(t *testing.T) {
	router, native, legacy := newRouter()

	// The rolling-upgrade case: a dispatch published before the kind field
	// existed. It must route natively, which is where it was always going
	// to go, rather than being refused as unroutable.
	if err := router.Execute(context.Background(), wire.DispatchPayload{JobID: "old"}); err != nil {
		t.Fatalf("Execute with no kind: %v", err)
	}
	if len(native.got) != 1 {
		t.Errorf("a kind-less dispatch did not reach the native adapter")
	}
	if len(legacy.got) != 0 {
		t.Errorf("a kind-less dispatch reached the legacy adapter")
	}

	// Whitespace is not a kind either. A payload carrying " " must not be
	// treated as a distinct, unroutable kind.
	if err := router.Execute(context.Background(), wire.DispatchPayload{JobID: "blank", Kind: "   "}); err != nil {
		t.Errorf("Execute with a blank kind: %v", err)
	}
}

func TestRouter_RefusesAKindItCannotRunAndSaysWhatItCan(t *testing.T) {
	router, native, legacy := newRouter()

	err := router.Execute(context.Background(), wire.DispatchPayload{JobID: "j", Kind: "terraform"})
	if !errors.Is(err, routing.ErrNoAdapter) {
		t.Fatalf("Execute of an unroutable kind returned %v, want ErrNoAdapter", err)
	}

	// The refusal names what this binary can run, which turns "no adapter"
	// into a diagnosis rather than a dead end.
	for _, want := range []string{"terraform", "playbook", "runbook"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}

	// And nothing ran. Falling back to any adapter would be worse than the
	// refusal: running a playbook through the native engine, or a runbook
	// through a sandbox, are both real executions of the wrong thing.
	if len(native.got) != 0 || len(legacy.got) != 0 {
		t.Error("an unroutable dispatch was executed by an adapter anyway")
	}
}

func TestRouter_AKindWhoseAdapterWasNotComposedIsUnroutableRatherThanFatal(t *testing.T) {
	// A deployment that has never run Ansible composes no legacy adapter.
	// Refusing to build a Router would stop the Runner booting over a
	// capability it does not use, so the kind is unroutable at dispatch
	// time instead.
	native := &recordingAdapter{name: "native"}
	router := routing.New(map[string]routing.Executor{"native": native})

	if !router.Routable("runbook") {
		t.Error("a Runner with the native adapter cannot route runbooks")
	}
	if router.Routable("playbook") {
		t.Error("a Runner with no legacy adapter reports playbooks as routable")
	}
	if kinds := router.Kinds(); len(kinds) != 1 || kinds[0] != "runbook" {
		t.Errorf("the Router reports %v routable, want runbook alone", kinds)
	}

	if err := router.Execute(context.Background(), wire.DispatchPayload{Kind: "playbook"}); !errors.Is(err, routing.ErrNoAdapter) {
		t.Errorf("a playbook dispatch to a Runner with no legacy adapter returned %v, want ErrNoAdapter", err)
	}
}

func TestRouter_ANilAdapterIsTreatedAsAbsent(t *testing.T) {
	// A composition root that failed to build one adapter and passed nil
	// rather than omitting the key. Calling through it would panic inside
	// the message handler, which is the worst place to discover a wiring
	// mistake.
	router := routing.New(map[string]routing.Executor{"native": nil, "legacy": nil})

	if kinds := router.Kinds(); len(kinds) != 0 {
		t.Errorf("a Router built from nil adapters reports %v routable", kinds)
	}
	if err := router.Execute(context.Background(), wire.DispatchPayload{Kind: "runbook"}); !errors.Is(err, routing.ErrNoAdapter) {
		t.Errorf("a nil adapter returned %v, want ErrNoAdapter rather than a panic", err)
	}
}

func TestResolve_IsTheOneDefinitionOfAnEmptyKind(t *testing.T) {
	for _, in := range []string{"", "   ", "\t"} {
		if got := routing.Resolve(in); got != routing.DefaultKind {
			t.Errorf("Resolve(%q) = %q, want %q", in, got, routing.DefaultKind)
		}
	}
	if got := routing.Resolve(" playbook "); got != "playbook" {
		t.Errorf("Resolve(%q) = %q, want the trimmed kind", " playbook ", got)
	}
}
