// The refusals of the Crawl tier's sdk.AIProject that a healthy project
// never reaches: files that are not files, a project that is gone, a
// directory where a result belongs, and a credential store that fails.
package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/promptsign"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestAIProject_RefusesWhatIsNotAReadableFile: a trusted-keys file over its
// limit, a prompt or signature that is a directory, and a project that no
// longer exists are errors, not an absent file.
func TestAIProject_RefusesWhatIsNotAReadableFile(t *testing.T) {
	rc, dir := projectContext(t, nil, nil)
	writeFile(t, filepath.Join(dir, AITrustedKeysFile), strings.Repeat("#", promptsign.MaxKeysFileBytes+1))
	if _, _, err := rc.AITrustedKeys(); err == nil || !strings.Contains(err.Error(), "is over") {
		t.Errorf("an oversized keys file = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, AIPromptsDir, "a.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := rc.AIPrompt("a"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a prompt that is a directory = %v", err)
	}
	writeFile(t, filepath.Join(dir, AIPromptsDir, "b.json"), `{}`)
	if err := os.MkdirAll(filepath.Join(dir, AIPromptsDir, "b.json.sig"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := rc.AIPrompt("b"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a signature that is a directory = %v", err)
	}

	gone, goneDir := projectContext(t, nil, nil)
	if err := os.RemoveAll(goneDir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := gone.AITrustedKeys(); err == nil {
		t.Error("a project that is gone read as having no keys")
	}
	if err := gone.AIWrite("ai/usage/u.jsonl", []byte("x"), true); err == nil {
		t.Error("a write into a project that is gone succeeded")
	}
}

// TestAIProject_RefusesAResultThatCannotBeWritten: a directory where the
// result file belongs, and a result file that exists, are errors the
// caller sees, and nothing is written over.
func TestAIProject_RefusesAResultThatCannotBeWritten(t *testing.T) {
	rc, dir := projectContext(t, nil, nil)
	if err := os.MkdirAll(filepath.Join(dir, "ai", "bench", "d.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := rc.AIWrite("ai/bench/d.json", []byte("x"), false); err == nil {
		t.Error("a result written where a directory is")
	}
	if err := rc.AIWrite("ai/bench/e.json", nil, false); err != nil {
		t.Fatalf("claiming a result file = %v", err)
	}
	if err := rc.AIWrite("ai/bench/e.json", []byte("{}\n"), true); err != nil {
		t.Fatalf("filling a claimed result file = %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "ai", "bench", "e.json")); string(got) != "{}\n" {
		t.Errorf("the claimed file holds %q", got)
	}
	if err := rc.AIWrite("ai/bench/e.json", nil, false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("claiming a taken name = %v", err)
	}
}

// failingCredentials is a store whose lookup fails for a reason other than
// the credential being absent.
type failingCredentials struct{}

func (failingCredentials) Lookup(context.Context, string) (credential.Credential, error) {
	return credential.Credential{}, errors.New("the credential file is unreadable")
}

// TestAIProject_ProviderRefusals: no inventory, an unknown name, a store
// that fails, and no store at all (a keyless provider is still handed out).
func TestAIProject_ProviderRefusals(t *testing.T) {
	e := &collectionActionExecutor{}
	WithAIProject(t.TempDir(), nil, nil)(e)
	none := NewRunbookContext(nil).(*runbookContext)
	e.lendAIProject(none)
	if _, _, err := none.AIProvider(context.Background(), "x"); !errors.Is(err, sdk.ErrNoAIProject) {
		t.Errorf("no inventory = %v", err)
	}
	devices := devicesByName{"local": providerDevice(t, "local", true)}
	rc, _ := projectContext(t, devices, nil)
	if _, _, err := rc.AIProvider(context.Background(), "nobody"); err == nil || !strings.Contains(err.Error(), "provider nobody") {
		t.Errorf("an unknown provider = %v", err)
	}
	if item, secrets, err := rc.AIProvider(context.Background(), "local"); err != nil || item == nil || len(secrets) != 0 {
		t.Errorf("a provider with no credential store = %v, %v, %v", item, secrets, err)
	}
	failing, _ := projectContext(t, devices, failingCredentials{})
	if _, _, err := failing.AIProvider(context.Background(), "local"); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Errorf("a failing credential store = %v", err)
	}
}

// TestAIProject_NoProjectHoldsNothing: reviews, version registers and
// dropped parameters of a context with no project say so, and a drop is
// not remembered.
func TestAIProject_NoProjectHoldsNothing(t *testing.T) {
	rc := NewRunbookContext(nil).(*runbookContext)
	if _, _, err := rc.Decided("k"); !errors.Is(err, sdk.ErrNoAIProject) {
		t.Errorf("Decided = %v", err)
	}
	if _, err := rc.Hold(context.Background(), sdk.Review{Key: "k"}); !errors.Is(err, sdk.ErrNoAIProject) {
		t.Errorf("Hold = %v", err)
	}
	rc.AIDrop("p", "m", "temperature")
	if got := rc.AIDropped("p", "m"); len(got) != 0 {
		t.Errorf("a drop with no project was remembered: %v", got)
	}
	with, _ := projectContext(t, nil, nil)
	if v, err := with.AIPromptVersions(); err != nil || v == nil {
		t.Errorf("AIPromptVersions = %v, %v", v, err)
	}
}
