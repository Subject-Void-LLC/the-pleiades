// Package model implements ai.model.list, load, unload, pull and remove:
// the models an Ollama host keeps on disk and in memory
// (docs/design/ai.md). The changing methods run only against a host whose
// record declares the single_resident guard, and keep to it: at most one
// model in memory, a load unloading every other first. They speak only
// Ollama's HTTP API, so nothing here can restart, stop or signal the host,
// or the machine or distribution it runs in.
package model

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/aitask"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The methods' names.
const (
	fqcnList   = "ai.model.list"
	fqcnLoad   = "ai.model.load"
	fqcnUnload = "ai.model.unload"
	fqcnPull   = "ai.model.pull"
	fqcnRemove = "ai.model.remove"
)

// Parameters.
const (
	paramModel   = "model"
	paramTimeout = "timeout"
)

// guardSingleResident is the host guard the changing methods need.
const guardSingleResident = "single_resident"

// Bounds.
const (
	defaultTimeout = 10 * time.Minute
	maxTimeout     = 6 * time.Hour
)

// host is an Ollama host a task acts on.
type host struct {
	name string
	ep   aiprovider.Endpoint
}

// hostFor resolves device into an Ollama host, refusing another kind and,
// when guarded is set, a host whose record does not declare the guard.
func hostFor(rc sdk.RunbookContext, device inventory.InventoryItem, guarded bool) (host, error) {
	t, err := aitask.TargetFor(device, rc.InjectSecrets())
	if err != nil {
		return host{}, err
	}
	if t.Provider.Kind.Name() != "ollama" {
		return host{}, fmt.Errorf("%s is a %s provider; these methods manage an Ollama host's models", t.Name, t.Provider.Kind.Name())
	}
	if guarded {
		dev, _ := device.(capability.AIProviderCapable)
		if dev == nil || dev.AIHostGuard() != guardSingleResident {
			return host{}, fmt.Errorf("%s does not declare ai_host_guard: %s, so its models are not changed: set it on the record to let these methods load, unload, pull and remove models there, one in memory at a time", t.Name, guardSingleResident)
		}
	}
	if err := sdk.RecordWarnings(rc, t.Provider.Warnings); err != nil {
		return host{}, err
	}
	return host{name: t.Name, ep: t.Provider.Endpoint}, nil
}

// Normalize writes a model's name as Ollama lists it: with :latest when it
// names no tag.
func Normalize(name string) string {
	last := name[strings.LastIndex(name, "/")+1:]
	if !strings.Contains(last, ":") {
		return name + ":latest"
	}
	return name
}

// modelParam reads the model to act on.
func modelParam(params map[string]any) (string, error) {
	m, err := sdk.RequiredStringParam(params, paramModel)
	if err != nil {
		return "", err
	}
	if strings.ContainsFunc(m, func(r rune) bool { return r <= ' ' || r == 0x7f }) || len(m) > 256 {
		return "", fmt.Errorf("%s %q is not a model name", paramModel, m)
	}
	return Normalize(m), nil
}

// timeoutParam reads how long a change may take.
func timeoutParam(params map[string]any) (time.Duration, error) {
	raw, present := params[paramTimeout]
	if !present || raw == nil {
		return defaultTimeout, nil
	}
	text, _ := raw.(string)
	d, err := time.ParseDuration(text)
	if err != nil || d < time.Second || d > maxTimeout {
		return 0, fmt.Errorf("%s: %v is not a duration from 1s to %s", paramTimeout, raw, maxTimeout)
	}
	return d, nil
}

// names lists models' names, normalized.
func names(models []aiprovider.OllamaModel) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = Normalize(m.Name)
	}
	return out
}

// has reports whether list holds name.
func has(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

// strs is a string list as a return value.
func strs(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// present lists the models on disk and in memory.
func (h host) present(ctx context.Context) (disk, memory []string, err error) {
	d, err := aiprovider.OllamaModels(ctx, h.ep)
	if err != nil {
		return nil, nil, fmt.Errorf("listing models: %w", err)
	}
	m, err := aiprovider.OllamaResident(ctx, h.ep)
	if err != nil {
		return nil, nil, fmt.Errorf("listing models in memory: %w", err)
	}
	return names(d), names(m), nil
}

// manifest is what every model method declares.
func manifest(doc collection.Doc, rev collection.Reversibility) collection.Manifest {
	return collection.Manifest{
		SupportedTransports:  []string{capability.TransportHTTPS},
		RequiredCapabilities: []capability.Name{capability.NameAIProvider},
		ExecutionContext:     collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceRequired},
		EngineVersion:        ">=0.2.0",
		Status:               collection.StatusImplemented,
		SupportsCheck:        true,
		Reversibility:        rev,
		Doc:                  doc,
	}
}

// inverse is an undo of one of these methods, naming the model.
func inverse(fqcn, model string) sdk.Inverse {
	return sdk.Inverse{FQCN: fqcn, Params: map[string]any{paramModel: model}}
}

// undoes is every undo these methods record: one model method, with the
// model's name, an identifier the journal may keep.
func undoes(fqcns ...string) []sdk.InverseSpec {
	out := make([]sdk.InverseSpec, len(fqcns))
	for i, f := range fqcns {
		out[i] = sdk.InverseSpec{FQCN: f, Record: []string{paramModel}}
	}
	return out
}
