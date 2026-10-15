// What a run's project holds for model calls, which the ai methods ask for.
package sdk

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// ErrNoAIProject is what an AIProject answers when the run was given no
// project to read prompts and keys from or write usage into.
var ErrNoAIProject = errors.New("this run has no project for model calls to read prompts and trusted keys from or write usage into (the Walk tier does not carry one yet)")

// PromptVersions is the register of the newest version of each signed
// document's name already used, which a document older than is refused.
type PromptVersions interface {
	Newest(kind, name string) (int64, bool, error)
	// Use records that version of name was used, keeping the newest.
	Use(kind, name string, version int64) error
}

// AIProject is what a run's project holds for model calls (the ai
// methods): prompt bundles and the trusted keys that vouch for them, the
// register of versions already used, the files usage and bench results go
// to, and the other providers a routing table names. The Crawl tier
// implements it over the project directory; a context that does not
// implement it, or answers ErrNoAIProject, has none, and a method needing
// one refuses before any call.
type AIProject interface {
	// AITrustedKeys returns the project's trusted keys file, false when
	// it has none.
	AITrustedKeys() ([]byte, bool, error)
	// AIPrompt returns the prompt bundle named name and its detached
	// signature, sig nil when the bundle has none.
	AIPrompt(name string) (doc, sig []byte, err error)
	// AIPromptVersions is the project's version register.
	AIPromptVersions() (PromptVersions, error)
	// AIWrite writes data to rel, a file under the project's ai/usage or
	// ai/bench directory, appending when appendTo is set. Nothing else in
	// the project is writable through it: not a prompt, not the trusted
	// keys, not the inventory.
	AIWrite(rel string, data []byte, appendTo bool) error
	// AIProvider returns the onboarded ai_provider device named name and
	// its own credential, flattened as InjectSecrets flattens one. Any
	// other device is refused, so this never yields a machine's login.
	AIProvider(ctx context.Context, name string) (inventory.InventoryItem, map[string]string, error)
	// AIDropped returns the parameters a provider rejected earlier in the
	// run for one model, and AIDrop records another, so a dropped
	// parameter is never sent to that provider for that model again in
	// the run. Both are safe for concurrent use.
	AIDropped(provider, model string) []string
	AIDrop(provider, model, param string)
}
