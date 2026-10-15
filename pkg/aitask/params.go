// Package aitask: reading a task's call parameters.
package aitask

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The parameters a call takes.
const (
	ParamModel       = "model"
	ParamMessages    = "messages"
	ParamPrompt      = "prompt"
	ParamPromptFile  = "prompt_file"
	ParamSignature   = "signature"
	ParamInput       = "input"
	ParamTemperature = aiprovider.ParamTemperature
	ParamTopP        = aiprovider.ParamTopP
	ParamMaxTokens   = aiprovider.ParamMaxTokens
	ParamStop        = aiprovider.ParamStop
	ParamThinking    = aiprovider.ParamThinking
	ParamSchema      = aiprovider.ParamSchema
	ParamTimeout     = "timeout"
	ParamRetries     = "retries"
	ParamSigning     = "signing"
	ParamResidency   = "residency"
	ParamUsageSink   = "usage_sink"
)

// Bounds a call's parameters are held to.
const (
	DefaultTimeout   = 5 * time.Minute
	MaxTimeout       = time.Hour
	DefaultRetries   = 3
	MaxRetries       = 10
	maxTokensLimit   = 1_000_000
	maxStops         = 8
	maxMessages      = 256
	maxMessagesBytes = 1 << 20
)

// Options are a call's generation settings and how hard it tries.
type Options struct {
	Params  aiprovider.Params
	Schema  json.RawMessage
	schema  *aiprovider.Schema
	Timeout time.Duration
	Retries int
}

// ParseOptions reads temperature, top_p, max_tokens, stop, thinking,
// schema, timeout and retries, each held to its range.
func ParseOptions(params map[string]any) (Options, error) {
	o := Options{Timeout: DefaultTimeout, Retries: DefaultRetries}
	var err error
	if o.Params.Temperature, err = floatParam(params, ParamTemperature, 0, 2); err != nil {
		return o, err
	}
	if o.Params.TopP, err = floatParam(params, ParamTopP, 0, 1); err != nil {
		return o, err
	}
	if o.Params.MaxTokens, err = intRange(params, ParamMaxTokens, 1, maxTokensLimit); err != nil {
		return o, err
	}
	if o.Params.Thinking, err = intRange(params, ParamThinking, 1, maxTokensLimit); err != nil {
		return o, err
	}
	if o.Params.Stop, err = stops(params); err != nil {
		return o, err
	}
	if o.Schema, o.schema, err = schemaParam(params); err != nil {
		return o, err
	}
	if raw, present := params[ParamTimeout]; present && raw != nil {
		text, _ := raw.(string)
		d, err := time.ParseDuration(text)
		if err != nil || d < time.Second || d > MaxTimeout {
			return o, fmt.Errorf("%s: %v is not a duration from 1s to %s, such as \"90s\"", ParamTimeout, raw, MaxTimeout)
		}
		o.Timeout = d
	}
	if n, set, err := sdk.IntParam(params, ParamRetries); err != nil {
		return o, err
	} else if set {
		if n < 0 || n > MaxRetries {
			return o, fmt.Errorf("%s is %d; it must be from 0 to %d", ParamRetries, n, MaxRetries)
		}
		o.Retries = n
	}
	return o, nil
}

// ValidateAnswer holds a structured answer to the call's schema; nil when
// the call asked for none.
func (o Options) ValidateAnswer(answer string) error {
	if o.schema == nil {
		return nil
	}
	return o.schema.Validate([]byte(answer))
}

// floatParam reads a number from lo to hi, nil when absent.
func floatParam(params map[string]any, key string, lo, hi float64) (*float64, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return nil, nil
	}
	var f float64
	switch v := raw.(type) {
	case int:
		f = float64(v)
	case int64:
		f = float64(v)
	case float64:
		f = v
	default:
		return nil, fmt.Errorf("%s is %T, not a number", key, raw)
	}
	if math.IsNaN(f) || f < lo || f > hi {
		return nil, fmt.Errorf("%s is %v; it must be from %v to %v", key, f, lo, hi)
	}
	return &f, nil
}

// intRange reads a whole number from lo to hi, nil when absent.
func intRange(params map[string]any, key string, lo, hi int) (*int, error) {
	n, set, err := sdk.IntParam(params, key)
	if err != nil || !set {
		return nil, err
	}
	if n < lo || n > hi {
		return nil, fmt.Errorf("%s is %d; it must be from %d to %d", key, n, lo, hi)
	}
	return &n, nil
}

// stops reads stop: one sequence or a list of up to maxStops.
func stops(params map[string]any) ([]string, error) {
	if s, ok := params[ParamStop].(string); ok {
		if s == "" {
			return nil, fmt.Errorf("%s is empty", ParamStop)
		}
		return []string{s}, nil
	}
	list, set, err := sdk.StringSlice(params, ParamStop)
	if err != nil || !set {
		return nil, err
	}
	if len(list) == 0 || len(list) > maxStops {
		return nil, fmt.Errorf("%s holds %d sequences; it takes 1 to %d", ParamStop, len(list), maxStops)
	}
	for _, s := range list {
		if s == "" || len(s) > 256 {
			return nil, fmt.Errorf("%s holds an empty sequence or one over 256 bytes", ParamStop)
		}
	}
	return list, nil
}

// schemaParam reads schema, a mapping or its JSON text, refusing one the
// answer could not be checked against.
func schemaParam(params map[string]any) (json.RawMessage, *aiprovider.Schema, error) {
	raw, present := params[ParamSchema]
	if !present || raw == nil {
		return nil, nil, nil
	}
	var data []byte
	switch v := raw.(type) {
	case string:
		data = []byte(v)
	case map[string]any:
		var err error
		if data, err = json.Marshal(v); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", ParamSchema, err)
		}
	default:
		return nil, nil, fmt.Errorf("%s is a JSON schema: a mapping, or its JSON text", ParamSchema)
	}
	s, err := aiprovider.ParseSchema(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ParamSchema, err)
	}
	return data, s, nil
}

// sinkName is the form of a usage sink's file name.
var sinkName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}\.jsonl$`)

// ParseSink reads usage_sink, a file name under the project's ai/usage
// directory, returning its path there, or "" when absent.
func ParseSink(params map[string]any) (string, error) {
	name := sdk.StringParam(params, ParamUsageSink)
	if name == "" {
		return "", nil
	}
	if !sinkName.MatchString(name) {
		return "", fmt.Errorf("%s %q is not a file name ending in .jsonl (it is written under the project's ai/usage)", ParamUsageSink, name)
	}
	return "ai/usage/" + name, nil
}

// ParseResidency reads residency, a use's tag: local, hosted, or absent.
func ParseResidency(params map[string]any) (string, error) {
	tag := sdk.StringParam(params, ParamResidency)
	if err := CheckResidency(tag, "", ResidencyLocal); err != nil {
		return "", fmt.Errorf("%s: %w", ParamResidency, err)
	}
	return tag, nil
}

// ParseMessages reads messages: a list of {role, content}, role system,
// user or assistant, with at least one user message.
func ParseMessages(raw any) ([]aiprovider.Message, error) {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 || len(list) > maxMessages {
		return nil, fmt.Errorf("%s is a list of 1 to %d {role, content} mappings", ParamMessages, maxMessages)
	}
	out := make([]aiprovider.Message, 0, len(list))
	total, users := 0, 0
	for i, item := range list {
		m, ok := item.(map[string]any)
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		if !ok || len(m) != 2 || content == "" {
			return nil, fmt.Errorf("%s[%d] must be a mapping of role and content, both set", ParamMessages, i)
		}
		switch role {
		case "user":
			users++
		case "system", "assistant":
		default:
			return nil, fmt.Errorf("%s[%d]: role %q is not system, user or assistant", ParamMessages, i, role)
		}
		total += len(content)
		out = append(out, aiprovider.Message{Role: role, Content: content})
	}
	if total > maxMessagesBytes {
		return nil, fmt.Errorf("%s hold over %d bytes", ParamMessages, maxMessagesBytes)
	}
	if users == 0 {
		return nil, fmt.Errorf("%s hold no user message", ParamMessages)
	}
	return out, nil
}
