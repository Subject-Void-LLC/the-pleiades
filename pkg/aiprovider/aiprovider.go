// Package aiprovider calls language models through one interface,
// whatever their API: a Kind builds a provider's requests and reads its
// answers, and Call puts every call through the same rules (parameters a
// kind does not accept are ignored, ones a provider rejects are dropped and
// never resent, only retryable failures are retried, and every call ends in
// a usage record). The built-in kinds are ollama, anthropic, google and
// openai (any server speaking OpenAI's chat completions API); an external
// Collection registers another with Register and builds its methods on
// Call, so its calls are metered and refused the same way.
package aiprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"
)

// The parameters a call may set. A kind says which it accepts.
const (
	ParamTemperature = "temperature"
	ParamTopP        = "top_p"
	ParamMaxTokens   = "max_tokens"
	ParamStop        = "stop"
	ParamThinking    = "thinking"
	ParamSchema      = "schema"
)

// AllParams is every parameter a call may set, in a fixed order.
var AllParams = []string{ParamTemperature, ParamTopP, ParamMaxTokens, ParamStop, ParamThinking, ParamSchema}

// Endpoint is where a provider is and how a request reaches it.
type Endpoint struct {
	// Base is the provider's API base URL.
	Base *url.URL
	// Client sends the requests; its transport carries the device's TLS
	// and refuses a redirect off Base's origin.
	Client *http.Client
	// Key is the API key, or empty for a provider that takes none.
	Key string
}

// Message is one turn of a conversation.
type Message struct {
	// Role is system, user or assistant.
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Params are a call's settings; a nil field is unset and not sent.
type Params struct {
	Temperature *float64
	TopP        *float64
	MaxTokens   *int
	Stop        []string
	// Thinking is a token budget for a model that thinks before it
	// answers.
	Thinking *int
}

// Request is one chat call.
type Request struct {
	Model    string
	Messages []Message
	Params   Params
	// Schema, when set, asks for an answer that is JSON meeting it.
	Schema json.RawMessage
}

// Set is every parameter the request sets.
func (r Request) Set() []string {
	var out []string
	p := r.Params
	for _, s := range []struct {
		name string
		set  bool
	}{
		{ParamTemperature, p.Temperature != nil}, {ParamTopP, p.TopP != nil}, {ParamMaxTokens, p.MaxTokens != nil},
		{ParamStop, len(p.Stop) > 0}, {ParamThinking, p.Thinking != nil}, {ParamSchema, len(r.Schema) > 0},
	} {
		if s.set {
			out = append(out, s.name)
		}
	}
	return out
}

// Response is a call's answer.
type Response struct {
	Text         string
	InputTokens  int
	OutputTokens int
	// Reported is whether the provider said how many tokens the call took;
	// a figure it never sent is not zero tokens.
	Reported bool
	// Model is the model the provider says answered.
	Model string
	// StopReason is why the provider stopped, in its own words.
	StopReason string
}

// Kind is one provider API.
type Kind interface {
	// Name is the kind's name, as an ai_provider record names it.
	Name() string
	// Accepts reports whether the kind can send param.
	Accepts(param string) bool
	// WireName is param as the provider's API names it, which is how a
	// provider's refusal of a parameter is recognized.
	WireName(param string) string
	// NeedsKey reports whether a call needs an API key.
	NeedsKey() bool
	// Chat makes one streamed call, sending none of the parameters in
	// omit, and returns the whole answer. An error from the provider is an
	// *APIError; one after the answer began streaming says so (Streamed),
	// and is never retried.
	Chat(ctx context.Context, ep Endpoint, req Request, omit map[string]bool) (Response, error)
	// Models lists the models the provider serves.
	Models(ctx context.Context, ep Endpoint) ([]string, error)
}

// APIError is a provider's refusal or failure.
type APIError struct {
	// Status is the HTTP status, 0 for a failure before one.
	Status int
	// Message is the provider's own message, bounded.
	Message string
	// RetryAfter is what the provider asked to wait, or zero.
	RetryAfter time.Duration
	// Streamed is set when the failure came after the answer began.
	Streamed bool
	// Network is set for a failure to reach the provider at all.
	Network bool
}

func (e *APIError) Error() string {
	switch {
	case e.Network:
		return "the provider could not be reached: " + e.Message
	case e.Streamed:
		return fmt.Sprintf("the answer stopped partway (status %d): %s", e.Status, e.Message)
	}
	return fmt.Sprintf("status %d: %s", e.Status, e.Message)
}

// Retryable reports whether trying again could succeed: rate limits,
// overload and server errors, and a provider never reached; never a
// failure after the answer began, which could bill twice.
func (e *APIError) Retryable() bool {
	if e.Streamed {
		return false
	}
	if e.Network {
		return true
	}
	switch e.Status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
		return true
	}
	return false
}

var (
	mu    sync.RWMutex
	kinds = map[string]Kind{}
)

// Register adds a kind, refusing a name already registered.
func Register(k Kind) error {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := kinds[k.Name()]; dup {
		return fmt.Errorf("aiprovider: kind %q is already registered", k.Name())
	}
	kinds[k.Name()] = k
	return nil
}

// mustRegister registers a built-in kind.
func mustRegister(k Kind) {
	if err := Register(k); err != nil {
		panic(err)
	}
}

// Lookup returns the kind named name.
func Lookup(name string) (Kind, bool) {
	mu.RLock()
	defer mu.RUnlock()
	k, ok := kinds[name]
	return k, ok
}

// Names is every registered kind's name, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(kinds))
	for n := range kinds {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
