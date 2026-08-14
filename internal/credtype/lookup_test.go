package credtype_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// stubLookup is a source that returns a fixed value, so a test can exercise
// the reference-parsing and refusal logic without a filesystem.
type stubLookup struct {
	name  string
	value string
	err   error
	// got records the reference the source was handed, so a test can prove
	// only the part after the source name reaches it.
	got string
}

func (s *stubLookup) Name() string { return s.name }

func (s *stubLookup) Resolve(_ context.Context, reference string) (string, error) {
	s.got = reference
	return s.value, s.err
}

// TestDeclaredLookupsAreNamedAndRefuse covers the honesty pattern: a source
// this platform does not implement is NAMED, so a credential imported from
// AWX resolves to an error saying exactly that.
//
// The alternative, leaving them out, would produce "no such source", which
// reads to an operator as a typo in their own data rather than as a feature
// this platform has not built.
func TestDeclaredLookupsAreNamedAndRefuse(t *testing.T) {
	t.Parallel()

	declared := credtype.DeclaredLookups()
	if len(declared) != 8 {
		t.Fatalf("DeclaredLookups() returned %d sources, want the eight AWX names", len(declared))
	}

	// AWX's own namespaces, so an import maps onto them rather than failing
	// to find a source at all.
	want := map[string]bool{
		"hashivault_kv": true, "hashivault_ssh": true, "aws_secretsmanager": true,
		"azure_kv": true, "centrify_vault": true, "conjur": true,
		"thycotic_dsv": true, "thycotic_tss": true,
	}
	for _, d := range declared {
		if !want[d.Name()] {
			t.Errorf("DeclaredLookups() named %q, which is not an AWX source name", d.Name())
		}
		_, err := d.Resolve(context.Background(), "anything")
		if !errors.Is(err, credtype.ErrLookupNotImplemented) {
			t.Errorf("%s resolved without being implemented: %v", d.Name(), err)
		}
		if !strings.Contains(err.Error(), d.Name()) {
			t.Errorf("%s's refusal does not name the source: %v", d.Name(), err)
		}
	}
}

// TestLookupsResolveAReference covers the reference format and the two
// halves it splits into.
func TestLookupsResolveAReference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		reference string
		value     string
		sourceErr error
		want      string
		wantErr   error
		// wantGot is the reference the source should have been handed:
		// everything after the first colon, so a Vault path containing
		// colons survives intact.
		wantGot string
	}{
		{
			name:      "a plain reference",
			reference: "test:prod_api_token",
			value:     "a-real-secret",
			want:      "a-real-secret",
			wantGot:   "prod_api_token",
		},
		{
			name:      "only the FIRST colon separates the source",
			reference: "test:secret/data/prod#token:v2",
			value:     "a-real-secret",
			want:      "a-real-secret",
			wantGot:   "secret/data/prod#token:v2",
		},
		{
			name:      "a reference with no source is refused",
			reference: "prod_api_token",
			wantErr:   credtype.ErrLookupReference,
		},
		{
			name:      "a reference with an empty source is refused",
			reference: ":prod_api_token",
			wantErr:   credtype.ErrLookupReference,
		},
		{
			name:      "a reference with nothing after the source is refused",
			reference: "test:",
			wantErr:   credtype.ErrLookupReference,
		},
		{
			name:      "a reference naming an unknown source is refused",
			reference: "nonexistent:prod_api_token",
			wantErr:   credtype.ErrLookupUnknown,
		},
		{
			name:      "an empty resolved value is refused rather than injected",
			reference: "test:prod_api_token",
			value:     "",
			// A rotation that emptied a file would otherwise present as an
			// authentication failure against the target device.
			wantErr: credtype.ErrLookupReference,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := &stubLookup{name: "test", value: tt.value, err: tt.sourceErr}
			lookups, err := credtype.NewLookups(source)
			if err != nil {
				t.Fatalf("NewLookups() error = %v", err)
			}

			got, err := lookups.Resolve(context.Background(), "api_token", tt.reference)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want one matching %v", err, tt.wantErr)
				}
				// Every one of these reaches a job record, so it must name
				// the input an operator can act on.
				if !strings.Contains(err.Error(), "api_token") {
					t.Errorf("the error does not name the input: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Resolve() = %q, want %q", got, tt.want)
			}
			if source.got != tt.wantGot {
				t.Errorf("the source was handed %q, want %q", source.got, tt.wantGot)
			}
		})
	}
}

// TestARealSourceOverridesItsDeclaredPlaceholder is what lets a later phase
// ship a real hashivault_kv without touching a caller.
func TestARealSourceOverridesItsDeclaredPlaceholder(t *testing.T) {
	t.Parallel()

	lookups, err := credtype.NewLookups(&stubLookup{name: "hashivault_kv", value: "a-real-secret"})
	if err != nil {
		t.Fatalf("NewLookups() error = %v", err)
	}

	got, err := lookups.Resolve(context.Background(), "api_token", "hashivault_kv:secret/data/prod")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != "a-real-secret" {
		t.Errorf("Resolve() = %q, want the real source's value rather than the declared refusal", got)
	}
}

// TestNewLookupsRefusesAMalformedSet covers the wiring errors, which are
// composition-root mistakes and must fail at startup rather than at the
// first dispatch.
func TestNewLookupsRefusesAMalformedSet(t *testing.T) {
	t.Parallel()

	if _, err := credtype.NewLookups(nil); err == nil {
		t.Error("NewLookups() accepted a nil source")
	}
	if _, err := credtype.NewLookups(&stubLookup{name: ""}); err == nil {
		t.Error("NewLookups() accepted a source with no name")
	}
	if _, err := credtype.NewLookups(&stubLookup{name: "test"}, &stubLookup{name: "test"}); err == nil {
		t.Error("NewLookups() accepted two sources claiming one name")
	}
}

// TestLookupNamesReportsEverySource covers what a form or an API offers, so
// it lists the real set rather than restating it.
func TestLookupNamesReportsEverySource(t *testing.T) {
	t.Parallel()

	lookups, err := credtype.NewLookups(&stubLookup{name: "test"})
	if err != nil {
		t.Fatalf("NewLookups() error = %v", err)
	}

	names := lookups.Names()
	if len(names) != 9 {
		t.Fatalf("Names() = %v, want the eight declared sources plus the wired one", names)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("Names() is not sorted: %v", names)
		}
	}
}

// TestASourceFailureNamesTheSourceAndNotTheValue covers the error path an
// operator has to act on.
func TestASourceFailureNamesTheSourceAndNotTheValue(t *testing.T) {
	t.Parallel()

	lookups, err := credtype.NewLookups(&stubLookup{name: "test", err: errors.New("the mount is sealed")})
	if err != nil {
		t.Fatalf("NewLookups() error = %v", err)
	}

	_, err = lookups.Resolve(context.Background(), "api_token", "test:prod_api_token")
	if err == nil {
		t.Fatal("Resolve() succeeded against a failing source")
	}
	for _, want := range []string{"api_token", "test", "the mount is sealed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}
