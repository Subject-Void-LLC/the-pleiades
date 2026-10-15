// Tests for the Crawl tier's sdk.AIProject: what it reads, what it may
// write and where, and which devices it hands out.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// devicesByName is an inventory of fixed devices.
type devicesByName map[string]pkginventory.InventoryItem

func (d devicesByName) GetByName(_ context.Context, name string) (pkginventory.InventoryItem, error) {
	if item, ok := d[name]; ok {
		return item, nil
	}
	return nil, fmt.Errorf("no device named %s", name)
}

// providerDevice builds an ai_provider device, onboarded when onboarded is
// set (a discovery bound to its record, as onboarding writes one).
func providerDevice(t *testing.T, name string, onboarded bool) pkginventory.InventoryItem {
	t.Helper()
	props := map[string]pkginventory.PropertyValue{ai.KindProperty: "ollama", ai.APIBaseProperty: "http://127.0.0.1:11434", ai.ResidencyProperty: "local"}
	if onboarded {
		d := pkginventory.Discovery{Protocol: "https", Capabilities: []capability.Name{capability.NameAIProvider}}
		d.Binding = record.DiscoveryBinding(ai.TypeProvider, pkginventory.NewProperties(props))
		props[pkginventory.DiscoveredProperty] = d.Property()
	}
	item, err := ai.NewProvider(record.Record{ID: pkginventory.DeviceID(name), Name: name, Type: ai.TypeProvider, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

// projectContext is a runbook context over a project in a fresh
// directory, with devices and credentials.
func projectContext(t *testing.T, devices devicesByName, creds credential.Store) (*runbookContext, string) {
	t.Helper()
	dir := t.TempDir()
	e := &collectionActionExecutor{}
	WithAIProject(dir, devices, creds)(e)
	rc := NewRunbookContext(nil).(*runbookContext)
	e.lendAIProject(rc)
	return rc, dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestAIProject_NoneAnswersErrNoAIProject: a context the run gave no
// project says so from every call, so a method refuses rather than
// reading or writing somewhere else.
func TestAIProject_NoneAnswersErrNoAIProject(t *testing.T) {
	rc := NewRunbookContext(nil).(*runbookContext)
	if _, _, err := rc.AITrustedKeys(); !errors.Is(err, sdk.ErrNoAIProject) {
		t.Errorf("AITrustedKeys: %v", err)
	}
	if _, _, err := rc.AIPrompt("x"); !errors.Is(err, sdk.ErrNoAIProject) {
		t.Errorf("AIPrompt: %v", err)
	}
	if _, err := rc.AIPromptVersions(); !errors.Is(err, sdk.ErrNoAIProject) {
		t.Errorf("AIPromptVersions: %v", err)
	}
	if err := rc.AIWrite("ai/usage/u.jsonl", []byte("x"), true); !errors.Is(err, sdk.ErrNoAIProject) {
		t.Errorf("AIWrite: %v", err)
	}
	if _, _, err := rc.AIProvider(context.Background(), "x"); !errors.Is(err, sdk.ErrNoAIProject) {
		t.Errorf("AIProvider: %v", err)
	}
}

// TestAIProject_ReadsPromptsAndKeys: a bundle and its signature come from
// ai/prompts, the keys from ai/trusted-keys.yaml, and a name that is not
// one, or a link out of the project, is refused.
func TestAIProject_ReadsPromptsAndKeys(t *testing.T) {
	rc, dir := projectContext(t, nil, nil)
	if _, ok, err := rc.AITrustedKeys(); ok || err != nil {
		t.Errorf("no keys file: ok %v err %v", ok, err)
	}
	writeFile(t, filepath.Join(dir, "ai/trusted-keys.yaml"), "keys: []\n")
	if data, ok, err := rc.AITrustedKeys(); !ok || err != nil || string(data) != "keys: []\n" {
		t.Errorf("keys: %q %v %v", data, ok, err)
	}
	writeFile(t, filepath.Join(dir, "ai/prompts/triage.json"), `{"kind":"prompt"}`)
	doc, sig, err := rc.AIPrompt("triage")
	if err != nil || string(doc) != `{"kind":"prompt"}` || sig != nil {
		t.Errorf("unsigned prompt: %q %q %v", doc, sig, err)
	}
	writeFile(t, filepath.Join(dir, "ai/prompts/triage.json.sig"), `{"key":"k","sig":"s"}`)
	if _, sig, _ := rc.AIPrompt("triage"); string(sig) != `{"key":"k","sig":"s"}` {
		t.Errorf("signature %q", sig)
	}
	for _, name := range []string{"../triage", "Triage", "a/b", "", "x.json"} {
		if _, _, err := rc.AIPrompt(name); err == nil {
			t.Errorf("name %q was read", name)
		}
	}
	if _, _, err := rc.AIPrompt("missing"); err == nil || !strings.Contains(err.Error(), "ai/prompts/missing.json") {
		t.Errorf("missing: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "secret.json")
	writeFile(t, outside, `{}`)
	if err := os.Symlink(outside, filepath.Join(dir, "ai/prompts/escape.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := rc.AIPrompt("escape"); err == nil {
		t.Error("a link out of the project was read")
	}
}

// TestAIProject_WritesOnlyUsageAndBench: appends to ai/usage, writes a
// bench result once, and refuses every other path, a link standing in for
// either directory, and writing a result over another.
func TestAIProject_WritesOnlyUsageAndBench(t *testing.T) {
	rc, dir := projectContext(t, nil, nil)
	for range 2 {
		if err := rc.AIWrite("ai/usage/spend.jsonl", []byte("{}\n"), true); err != nil {
			t.Fatal(err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "ai/usage/spend.jsonl")); string(data) != "{}\n{}\n" {
		t.Errorf("usage %q", data)
	}
	if info, _ := os.Stat(filepath.Join(dir, "ai/usage/spend.jsonl")); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	if err := rc.AIWrite("ai/bench/run1.json", []byte("{}"), false); err != nil {
		t.Fatal(err)
	}
	if err := rc.AIWrite("ai/bench/run1.json", []byte("{}"), false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a second write: %v", err)
	}
	for _, rel := range []string{
		"ai/prompts/triage.json", "ai/trusted-keys.yaml", "../x.jsonl", "ai/usage/../bench/x.json", "ai/usage/x.json",
		"ai/usage/.x.jsonl", "ai/usage/sub/x.jsonl", "/ai/usage/x.jsonl", "inventory.yaml", "ai/usage/.jsonl",
	} {
		if err := rc.AIWrite(rel, []byte("x"), true); err == nil {
			t.Errorf("%s was written", rel)
		}
	}

	rc2, dir2 := projectContext(t, nil, nil)
	if err := os.MkdirAll(filepath.Join(dir2, "ai/prompts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("prompts", filepath.Join(dir2, "ai/bench")); err != nil {
		t.Fatal(err)
	}
	if err := rc2.AIWrite("ai/bench/new.json", []byte("{}"), false); err == nil {
		t.Error("a write went through ai/bench linked to ai/prompts")
	}
	if _, err := os.Stat(filepath.Join(dir2, "ai/prompts/new.json")); err == nil {
		t.Error("a prompt was created through the link")
	}
}

// TestAIProject_HandsOutOnlyOnboardedProviders: a provider comes with its
// own credential, which joins the masking set; a device that is not a
// provider, or a provider not yet onboarded, is refused.
func TestAIProject_HandsOutOnlyOnboardedProviders(t *testing.T) {
	server := record.Record{ID: "web", Name: "web", Type: "linux_server", Properties: map[string]pkginventory.PropertyValue{"host": "10.0.0.9"}}
	ctor, ok := record.LookupType("linux_server")
	if !ok {
		t.Skip("linux_server is not registered in this test binary")
	}
	web, err := ctor(server)
	if err != nil {
		t.Fatal(err)
	}
	devices := devicesByName{"local": providerDevice(t, "local", true), "fresh": providerDevice(t, "fresh", false), "web": web}
	creds := credentialsByName{"local": {Token: "sk-local"}, "web": {Username: "root", Password: "hunter2"}}
	rc, _ := projectContext(t, devices, creds)

	item, secrets, err := rc.AIProvider(context.Background(), "local")
	if err != nil || item.Name() != "local" || !slices.Contains(mapValues(secrets), "sk-local") {
		t.Fatalf("local: %v %v", secrets, err)
	}
	if !slices.Contains(rc.RegisteredSecrets(), "sk-local") {
		t.Error("the provider's key did not join the masking set")
	}
	if _, _, err := rc.AIProvider(context.Background(), "web"); err == nil || !strings.Contains(err.Error(), "not an ai_provider") {
		t.Errorf("web: %v", err)
	}
	if _, _, err := rc.AIProvider(context.Background(), "fresh"); err == nil || !strings.Contains(err.Error(), "onboard") {
		t.Errorf("fresh: %v", err)
	}
	if slices.Contains(rc.RegisteredSecrets(), "hunter2") {
		t.Error("a machine's password was handed out")
	}
}

// credentialsByName is a credential store of fixed credentials.
type credentialsByName map[string]credential.Credential

func (c credentialsByName) Lookup(_ context.Context, name string) (credential.Credential, error) {
	if cred, ok := c[name]; ok {
		return cred, nil
	}
	return credential.Credential{}, credential.ErrNotFound
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
