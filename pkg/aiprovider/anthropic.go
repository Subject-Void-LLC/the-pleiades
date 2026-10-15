// Package aiprovider: the anthropic kind, Anthropic's Messages API
// (POST /v1/messages, streamed as server-sent events).
package aiprovider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// anthropicVersion is the API version every request names.
const anthropicVersion = "2023-06-01"

// anthropicDefaultMaxTokens is sent when a call sets none: the API
// requires one.
const anthropicDefaultMaxTokens = 4096

// anthropicTool is the tool a structured answer is asked for through.
const anthropicTool = "answer"

type anthropic struct{}

func init() { mustRegister(anthropic{}) }

func (anthropic) Name() string   { return "anthropic" }
func (anthropic) NeedsKey() bool { return true }

func (anthropic) Accepts(string) bool { return true }

func (anthropic) WireName(p string) string {
	switch p {
	case ParamStop:
		return "stop_sequences"
	case ParamThinking:
		return "thinking"
	case ParamSchema:
		return "tool"
	}
	return p
}

// headers are the API's own: the key in x-api-key.
func (anthropic) headers(ep Endpoint) map[string]string {
	return map[string]string{"x-api-key": ep.Key, "anthropic-version": anthropicVersion}
}

func (a anthropic) Chat(ctx context.Context, ep Endpoint, req Request, omit map[string]bool) (Response, error) {
	body := map[string]any{"model": req.Model, "stream": true}
	var system []string
	var msgs []map[string]string
	for _, m := range req.Messages {
		if m.Role == "system" {
			system = append(system, m.Content)
			continue
		}
		msgs = append(msgs, map[string]string{"role": m.Role, "content": m.Content})
	}
	body["messages"] = msgs
	if len(system) > 0 {
		body["system"] = strings.Join(system, "\n\n")
	}
	p := req.Params
	maxTokens := anthropicDefaultMaxTokens
	if p.MaxTokens != nil && !omit[ParamMaxTokens] {
		maxTokens = *p.MaxTokens
	}
	if p.Thinking != nil && !omit[ParamThinking] {
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": *p.Thinking}
		if maxTokens <= *p.Thinking {
			maxTokens = *p.Thinking + anthropicDefaultMaxTokens
		}
	}
	body["max_tokens"] = maxTokens
	if p.Temperature != nil && !omit[ParamTemperature] {
		body["temperature"] = *p.Temperature
	}
	if p.TopP != nil && !omit[ParamTopP] {
		body["top_p"] = *p.TopP
	}
	if len(p.Stop) > 0 && !omit[ParamStop] {
		body["stop_sequences"] = p.Stop
	}
	structured := len(req.Schema) > 0 && !omit[ParamSchema]
	if structured {
		body["tools"] = []any{map[string]any{"name": anthropicTool, "description": "Give the answer.", "input_schema": json.RawMessage(req.Schema)}}
		body["tool_choice"] = map[string]any{"type": "tool", "name": anthropicTool}
	}
	resp, err := post(ctx, ep, join(ep.Base, "/v1/messages"), a.headers(ep), body)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out Response
	var text, toolInput answer
	err = sseEvents(resp.Body, func(event string, data []byte) error {
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Model string `json:"model"`
				Usage struct {
					InputTokens int `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return &APIError{Streamed: true, Message: "an event is not JSON: " + err.Error()}
		}
		switch ev.Type {
		case "message_start":
			out.Model, out.InputTokens = ev.Message.Model, ev.Message.Usage.InputTokens
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				return text.add(ev.Delta.Text)
			case "input_json_delta":
				return toolInput.add(ev.Delta.PartialJSON)
			}
		case "message_delta":
			out.OutputTokens = ev.Usage.OutputTokens
			if ev.Delta.StopReason != "" {
				out.StopReason = ev.Delta.StopReason
			}
		case "error":
			status := 500
			if ev.Error.Type == "overloaded_error" {
				status = 529
			}
			return &APIError{Status: status, Streamed: true, Message: ev.Error.Message}
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	out.Text = text.b.String()
	if structured {
		if toolInput.b.Len() == 0 {
			return Response{}, errors.New("the model gave no structured answer")
		}
		out.Text = toolInput.b.String()
	}
	return out, nil
}

func (a anthropic) Models(ctx context.Context, ep Endpoint) ([]string, error) {
	resp, err := get(ctx, ep, join(ep.Base, "/v1/models"), a.headers(ep))
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
