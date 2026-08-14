package file_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/lookup/file"
)

// newLookup builds a Lookup over a directory holding the given secrets.
func newLookup(t *testing.T, secrets map[string]string) (*file.Lookup, string) {
	t.Helper()

	dir := t.TempDir()
	for name, body := range secrets {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("failed to write fixture secret %q: %v", name, err)
		}
	}
	return file.New(dir), dir
}

// TestResolveReadsTheSecret covers the ordinary case and the one piece of
// content handling this source does.
func TestResolveReadsTheSecret(t *testing.T) {
	t.Parallel()

	const pem = "-----BEGIN PRIVATE KEY-----\nbody\n-----END PRIVATE KEY-----\n"

	lookup, _ := newLookup(t, map[string]string{
		"plain":          "a-real-secret",
		"with_newline":   "a-real-secret\n",
		"with_two":       "a-real-secret\n\n",
		"pem":            pem,
		"internal_lines": "line one\nline two",
	})

	tests := []struct {
		name      string
		reference string
		want      string
	}{
		{
			name:      "a secret with no trailing newline is returned as written",
			reference: "plain",
			want:      "a-real-secret",
		},
		{
			name: "exactly one trailing newline is stripped",
			// Every tool that writes a secret to a file adds one, and no
			// secret this platform injects ends in a newline that matters.
			reference: "with_newline",
			want:      "a-real-secret",
		},
		{
			name: "only one, so a deliberate blank line survives",
			// Stripping all of them would silently alter a value nobody
			// asked to have altered.
			reference: "with_two",
			want:      "a-real-secret\n",
		},
		{
			name: "a PEM body keeps its own trailing newline",
			// A PEM legitimately ends with one, and a key stripped of it is
			// a key some parsers refuse.
			reference: "pem",
			want:      strings.TrimSuffix(pem, "\n"),
		},
		{
			name:      "interior newlines are untouched",
			reference: "internal_lines",
			want:      "line one\nline two",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := lookup.Resolve(context.Background(), tt.reference)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Resolve() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResolveRefusesAnythingThatIsNotAPlainFilename is the traversal
// control, and it is stricter than joining and cleaning on purpose.
//
// Cleaning a caller-supplied path and then checking the result is inside
// the directory is correct, and it is the check people get subtly wrong,
// most often by forgetting that a symlink inside the directory can point
// outside it. Refusing every separator, every dot segment and every empty
// name means there is no traversal to check for.
func TestResolveRefusesAnythingThatIsNotAPlainFilename(t *testing.T) {
	t.Parallel()

	lookup, _ := newLookup(t, map[string]string{"plain": "a-real-secret"})

	references := []string{
		"",
		".",
		"..",
		"../etc/passwd",
		"../../../../etc/shadow",
		"subdir/secret",
		"/etc/passwd",
		"./plain",
		"plain\x00.txt",
	}

	for _, reference := range references {
		t.Run(strings.ReplaceAll(reference, "/", "_"), func(t *testing.T) {
			t.Parallel()

			if _, err := lookup.Resolve(context.Background(), reference); err == nil {
				t.Fatalf("Resolve(%q) succeeded, so a reference can leave its directory", reference)
			} else if !errors.Is(err, credtype.ErrLookupReference) {
				t.Errorf("Resolve(%q) error = %v, want one matching ErrLookupReference", reference, err)
			}
		})
	}
}

// TestResolveDistinguishesUnconfiguredFromMissing covers two conditions
// that lead to two different operator actions.
//
// "This controller has no external secrets directory" is a deployment
// change. "That secret is not in it" is a data change. One error for both
// sends people to the wrong one.
func TestResolveDistinguishesUnconfiguredFromMissing(t *testing.T) {
	t.Parallel()

	t.Run("no directory configured", func(t *testing.T) {
		t.Parallel()

		_, err := file.New("").Resolve(context.Background(), "plain")
		if !errors.Is(err, file.ErrNotConfigured) {
			t.Fatalf("error = %v, want one matching ErrNotConfigured", err)
		}
		// It has to say what to set, or an operator has to read the source.
		if !strings.Contains(err.Error(), file.EnvDirectory) {
			t.Errorf("the error does not name the variable to set: %v", err)
		}
	})

	t.Run("a directory with nothing in it", func(t *testing.T) {
		t.Parallel()

		lookup, _ := newLookup(t, nil)
		_, err := lookup.Resolve(context.Background(), "plain")
		if !errors.Is(err, credtype.ErrLookupReference) {
			t.Fatalf("error = %v, want one matching ErrLookupReference", err)
		}
		if errors.Is(err, file.ErrNotConfigured) {
			t.Error("a missing secret was reported as a missing configuration")
		}
	})
}

// TestResolveNeverNamesTheDirectory covers what an error may say.
//
// A reference is a pointer rather than a secret and naming it helps. The
// directory names the deployment's own filesystem layout, and this error
// reaches a job record an API caller reads.
func TestResolveNeverNamesTheDirectory(t *testing.T) {
	t.Parallel()

	lookup, dir := newLookup(t, nil)

	_, err := lookup.Resolve(context.Background(), "prod_api_token")
	if err == nil {
		t.Fatal("Resolve() succeeded for a secret that does not exist")
	}
	if strings.Contains(err.Error(), dir) {
		t.Errorf("the error names the secrets directory: %v", err)
	}
	if !strings.Contains(err.Error(), "prod_api_token") {
		t.Errorf("the error does not name the reference: %v", err)
	}
}

// TestResolveRefusesWhatIsNotARegularFile covers the shapes that would
// either block the dispatch path forever or return something that is not a
// secret.
func TestResolveRefusesWhatIsNotARegularFile(t *testing.T) {
	t.Parallel()

	lookup, dir := newLookup(t, nil)
	if err := os.Mkdir(filepath.Join(dir, "a_directory"), 0o700); err != nil {
		t.Fatalf("failed to create the fixture directory: %v", err)
	}

	if _, err := lookup.Resolve(context.Background(), "a_directory"); err == nil {
		t.Fatal("Resolve() succeeded against a directory")
	} else if !strings.Contains(err.Error(), "regular file") {
		t.Errorf("the refusal does not say what was wrong: %v", err)
	}
}

// TestResolveRefusesAnOversizedFile covers the bound, which exists because
// the reference comes from a database row and the file it names is whatever
// happens to be on disk: without one, a reference pointing at a large file
// is read into memory once per device in a fan-out.
func TestResolveRefusesAnOversizedFile(t *testing.T) {
	t.Parallel()

	lookup, dir := newLookup(t, nil)
	oversized := make([]byte, (1<<20)+1)
	for i := range oversized {
		oversized[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(dir, "huge"), oversized, 0o600); err != nil {
		t.Fatalf("failed to write the oversized fixture: %v", err)
	}

	if _, err := lookup.Resolve(context.Background(), "huge"); err == nil {
		t.Fatal("Resolve() read a file past the bound")
	}

	// And exactly at the bound still works, so the check is a ceiling
	// rather than an off-by-one that rejects a legal secret.
	atLimit := make([]byte, 1<<20)
	for i := range atLimit {
		atLimit[i] = 'y'
	}
	if err := os.WriteFile(filepath.Join(dir, "at_limit"), atLimit, 0o600); err != nil {
		t.Fatalf("failed to write the at-limit fixture: %v", err)
	}
	got, err := lookup.Resolve(context.Background(), "at_limit")
	if err != nil {
		t.Fatalf("Resolve() refused a secret exactly at the bound: %v", err)
	}
	if len(got) != 1<<20 {
		t.Errorf("Resolve() returned %d bytes, want the whole secret", len(got))
	}
}

// TestFromEnvironmentReadsTheConfiguredDirectory covers the constructor a
// composition root actually calls.
func TestFromEnvironmentReadsTheConfiguredDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plain"), []byte("a-real-secret\n"), 0o600); err != nil {
		t.Fatalf("failed to write the fixture secret: %v", err)
	}
	t.Setenv(file.EnvDirectory, dir)

	got, err := file.FromEnvironment().Resolve(context.Background(), "plain")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != "a-real-secret" {
		t.Errorf("Resolve() = %q, want the fixture secret", got)
	}
}

// TestNameIsTheSourceKey pins the string a reference selects this source
// with, since it is written into stored data and cannot change quietly.
func TestNameIsTheSourceKey(t *testing.T) {
	t.Parallel()

	if file.Name != "file" {
		t.Errorf("file.Name = %q, want %q", file.Name, "file")
	}
	if got := file.New("/tmp").Name(); got != file.Name {
		t.Errorf("Name() = %q, want %q", got, file.Name)
	}
}
