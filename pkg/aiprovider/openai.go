// Package aiprovider: the openai kind, any server speaking OpenAI's chat
// completions API (POST {base}/chat/completions, streamed as server-sent
// events): OpenAI, and Alibaba, Mistral, Groq, xAI, vLLM and LM Studio
// at their own base URLs. The base URL includes the version path (/v1).
package aiprovider

import (
	"bytes"
	"context"
	"encoding/json"
)

type openai struct{}

func init() { mustRegister(openai{}) }

func (openai) Name() string   { return "openai" }
func (openai) NeedsKey() bool { return false }

// Accepts is every parameter but thinking, which this API has no token
// budget for.
func (openai) Accepts(p string) bool { return p != ParamThinking }

func (openai) WireName(p string) string {
	if p == ParamSchema {
		return "response_format"
	}
	return p
}

// headers send the key as a bearer token, when there is one: a local
// server (vLLM, LM Studio) often takes none.
func (openai) headers(ep Endpoint) map[string]string {
	if ep.Key == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + ep.Key}
}

func (o openai) Chat(ctx context.Context, ep Endpoint, req Request, omit map[string]bool) (Response, error) {
	body := map[string]any{"model": req.Model, "messages": req.Messages, "stream": true, "stream_options": map[string]bool{"include_usage": true}}
	p := req.Params
	if p.Temperature != nil && !omit[ParamTemperature] {
		body["temperature"] = *p.Temperature
	}
	if p.TopP != nil && !omit[ParamTopP] {
		body["top_p"] = *p.TopP
	}
	if p.MaxTokens != nil && !omit[ParamMaxTokens] {
		body["max_tokens"] = *p.MaxTokens
	}
	if len(p.Stop) > 0 && !omit[ParamStop] {
		body["stop"] = p.Stop
	}
	if len(req.Schema) > 0 && !omit[ParamSchema] {
		body["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "answer", "schema": json.RawMessage(req.Schema), "strict": true}}
	}
	resp, err := post(ctx, ep, join(ep.Base, "/chat/completions"), o.headers(ep), body)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out Response
	var text answer
	finished := false
	err = sseEvents(resp.Body, func(_ string, data []byte) error {
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			finished = true
			return nil
		}
		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &chunk); err != nil {
			return &APIError{Streamed: true, Message: "a chunk is not JSON: " + err.Error()}
		}
		if chunk.Error != nil {
			return &APIError{Status: 500, Streamed: true, Message: bound(chunk.Error.Message)}
		}
		if chunk.Model != "" {
			out.Model = chunk.Model
		}
		if chunk.Usage != nil {
			out.InputTokens, out.OutputTokens, out.Reported = chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens, true
		}
		for _, c := range chunk.Choices {
			if c.FinishReason != "" {
				out.StopReason, finished = c.FinishReason, true
			}
			if err := text.add(c.Delta.Content); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	if !finished {
		return Response{}, errTruncated()
	}
	out.Text = text.b.String()
	return out, nil
}

func (o openai) Models(ctx context.Context, ep Endpoint) ([]string, error) {
	resp, err := get(ctx, ep, join(ep.Base, "/models"), o.headers(ep))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := decodeBounded(resp, &list); err != nil {
		return nil, err
	}
	var out []string
	for _, m := range list.Data {
		out = append(out, m.ID)
	}
	return out, nil
}
