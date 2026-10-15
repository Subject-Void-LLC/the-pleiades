// The Crawl tier's sdk.AIProject: what the ai methods read from and write
// into the project directory a run was started in.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/promptsign"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Where a project keeps what model calls read and write, relative to it.
const (
	AITrustedKeysFile = "ai/trusted-keys.yaml"
	AIPromptsDir      = "ai/prompts"
	AIVersionsFile    = ".pleiades/prompt-versions.json"
)

// aiWritable are the directories AIWrite may write into, and the file
// suffixes each takes.
var aiWritable = map[string][]string{"ai/usage": {".jsonl"}, "ai/bench": {".json"}}

// maxSignatureBytes bounds a detached signature file.
const maxSignatureBytes = 4 << 10

// promptName is the form of a prompt bundle's name.
var promptName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// providerInventory finds a device by name.
type providerInventory interface {
	GetByName(ctx context.Context, name string) (pkginventory.InventoryItem, error)
}

// aiProject is sdk.AIProject over one project directory.
type aiProject struct {
	dir         string
	devices     providerInventory
	credentials credential.Store
	versions    *promptsign.FileVersions
	// dropped is, per provider and model, the parameters rejected this run.
	mu      sync.Mutex
	dropped map[[2]string]map[string]bool
}

// WithAIProject lets every Collection method of the run read prompt
// bundles and trusted keys from the project in dir, keep the version
// register there, write usage and bench results under its ai/usage and
// ai/bench directories, and reach the onboarded ai_provider devices of
// devices with their own credentials from credentials (sdk.AIProject).
func WithAIProject(dir string, devices providerInventory, credentials credential.Store) CollectionActionExecutorOption {
	return func(e *collectionActionExecutor) {
		e.ai = &aiProject{
			dir: dir, devices: devices, credentials: credentials,
			versions: &promptsign.FileVersions{Path: filepath.Join(dir, filepath.FromSlash(AIVersionsFile))},
		}
	}
}

// readBounded reads rel from the project, at most limit bytes, through an
// os.Root so no path or link leaves the project.
func (p *aiProject) readBounded(rel string, limit int64) ([]byte, bool, error) {
	root, err := os.OpenRoot(p.dir)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s is not a regular file", rel)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return nil, false, fmt.Errorf("%s is over %d bytes", rel, limit)
	}
	return data, true, nil
}

func (p *aiProject) trustedKeys() ([]byte, bool, error) {
	return p.readBounded(AITrustedKeysFile, promptsign.MaxKeysFileBytes)
}

func (p *aiProject) prompt(name string) ([]byte, []byte, error) {
	if !promptName.MatchString(name) {
		return nil, nil, fmt.Errorf("%q is not a prompt name: lower-case letters, digits, - and _", name)
	}
	rel := path.Join(AIPromptsDir, name+".json")
	doc, ok, err := p.readBounded(rel, promptsign.MaxDocumentBytes)
	switch {
	case err != nil:
		return nil, nil, err
	case !ok:
		return nil, nil, fmt.Errorf("the project has no prompt %s (%s)", name, rel)
	}
	sig, _, err := p.readBounded(rel+".sig", maxSignatureBytes)
	if err != nil {
		return nil, nil, err
	}
	return doc, sig, nil
}

func (p *aiProject) write(rel string, data []byte, appendTo bool) error {
	clean := path.Clean(rel)
	dir, file := path.Split(clean)
	dir = strings.TrimSuffix(dir, "/")
	suffixes, ok := aiWritable[dir]
	if !ok || clean != rel || file == "" || strings.HasPrefix(file, ".") {
		return fmt.Errorf("%q is not a file directly under ai/usage or ai/bench", rel)
	}
	if !hasSuffix(file, suffixes) {
		return fmt.Errorf("%q must end in %s", rel, strings.Join(suffixes, " or "))
	}
	root, err := os.OpenRoot(p.dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	// Neither ai nor its subdirectory may be a link: one could point the
	// write at a prompt, the trusted keys or the inventory.
	for _, d := range []string{"ai", dir} {
		info, err := root.Lstat(d)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := root.Mkdir(d, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
		case err != nil:
			return err
		case !info.IsDir():
			return fmt.Errorf("%s is not a directory (a link is refused)", d)
		}
	}
	sub, err := root.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = sub.Close() }()
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if appendTo {
		flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	f, err := sub.OpenFile(file, flags, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists, and a result is never written over", rel)
	}
	if err != nil {
		return err
	}
	// One write, so concurrent appends from a fanned-out task never
	// interleave within a line.
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// hasSuffix reports whether name ends in one of suffixes.
func hasSuffix(name string, suffixes []string) bool {
	for _, s := range suffixes {
		if strings.HasSuffix(name, s) && len(name) > len(s) {
			return true
		}
	}
	return false
}

func (p *aiProject) provider(ctx context.Context, name string) (pkginventory.InventoryItem, map[string]string, error) {
	if p.devices == nil {
		return nil, nil, sdk.ErrNoAIProject
	}
	item, err := p.devices.GetByName(ctx, name)
	if err != nil {
		return nil, nil, fmt.Errorf("provider %s: %w", name, err)
	}
	if _, ok := item.(capability.AIProviderCapable); !ok {
		return nil, nil, fmt.Errorf("%s is not an ai_provider device", name)
	}
	if !item.HasCapability(capability.NameAIProvider) {
		return nil, nil, fmt.Errorf("provider %s has not been onboarded (pleiades onboard %s)", name, name)
	}
	if p.credentials == nil {
		return item, map[string]string{}, nil
	}
	cred, err := p.credentials.Lookup(ctx, name)
	switch {
	case errors.Is(err, credential.ErrNotFound):
		return item, map[string]string{}, nil
	case err != nil:
		return nil, nil, fmt.Errorf("resolve credential for provider %s: %w", name, err)
	}
	return item, credential.Flatten(cred), nil
}

// AITrustedKeys implements sdk.AIProject.
func (c *runbookContext) AITrustedKeys() ([]byte, bool, error) {
	if c.ai == nil {
		return nil, false, sdk.ErrNoAIProject
	}
	return c.ai.trustedKeys()
}

// AIPrompt implements sdk.AIProject.
func (c *runbookContext) AIPrompt(name string) ([]byte, []byte, error) {
	if c.ai == nil {
		return nil, nil, sdk.ErrNoAIProject
	}
	return c.ai.prompt(name)
}

// AIPromptVersions implements sdk.AIProject.
func (c *runbookContext) AIPromptVersions() (sdk.PromptVersions, error) {
	if c.ai == nil {
		return nil, sdk.ErrNoAIProject
	}
	return c.ai.versions, nil
}

// AIWrite implements sdk.AIProject.
func (c *runbookContext) AIWrite(rel string, data []byte, appendTo bool) error {
	if c.ai == nil {
		return sdk.ErrNoAIProject
	}
	return c.ai.write(rel, data, appendTo)
}

// AIProvider implements sdk.AIProject. The provider's secrets join the
// run's masking set the moment they are handed out, as a database
// connection's password does.
func (c *runbookContext) AIProvider(ctx context.Context, name string) (pkginventory.InventoryItem, map[string]string, error) {
	if c.ai == nil {
		return nil, nil, sdk.ErrNoAIProject
	}
	item, secrets, err := c.ai.provider(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	for _, v := range credentialSecrets(secrets) {
		c.RegisterSecret(v)
	}
	return item, secrets, nil
}

// AIDropped implements sdk.AIProject.
func (c *runbookContext) AIDropped(provider, model string) []string {
	if c.ai == nil {
		return nil
	}
	c.ai.mu.Lock()
	defer c.ai.mu.Unlock()
	out := make([]string, 0, len(c.ai.dropped[[2]string{provider, model}]))
	for p := range c.ai.dropped[[2]string{provider, model}] {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// AIDrop implements sdk.AIProject.
func (c *runbookContext) AIDrop(provider, model, param string) {
	if c.ai == nil {
		return
	}
	c.ai.mu.Lock()
	defer c.ai.mu.Unlock()
	if c.ai.dropped == nil {
		c.ai.dropped = map[[2]string]map[string]bool{}
	}
	key := [2]string{provider, model}
	if c.ai.dropped[key] == nil {
		c.ai.dropped[key] = map[string]bool{}
	}
	c.ai.dropped[key][param] = true
}

// lendAIProject gives rc the run's project, when it has one.
func (e *collectionActionExecutor) lendAIProject(rc sdk.RunbookContext) {
	if e.ai == nil {
		return
	}
	if c, ok := rc.(*runbookContext); ok {
		c.ai = e.ai
	}
}
