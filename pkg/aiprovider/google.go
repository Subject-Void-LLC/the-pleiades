// Package aiprovider: the google kind, the Gemini API
// (POST /v1beta/models/{model}:streamGenerateContent?alt=sse).
package aiprovider

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

type google struct{}

func init() { mustRegister(google{}) }

func (google) Name() string        { return "google" }
func (google) NeedsKey() bool      { return true }
func (google) Accepts(string) bool { return true }

func (google) WireName(p string) string {
	switch p {
	case ParamTopP:
		return "topP"
	case ParamMaxTokens:
		return "maxOutputTokens"
	case ParamStop:
		return "stopSequences"
	case ParamThinking:
		return "thinkingBudget"
	case ParamSchema:
		return "responseSchema"
	}
	return p
}

// headers send the key in x-goog-api-key, never in the URL, where it
// would be logged by anything that logs URLs.
func (google) headers(ep Endpoint) map[string]string {
	return map[string]string{"x-goog-api-key": ep.Key}
}

func (g google) Chat(ctx context.Context, ep Endpoint, req Request, omit map[string]bool) (Response, error) {
	var contents []map[string]any
	var system []string
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			system = append(system, m.Content)
		case "assistant":
			contents = append(contents, map[string]any{"role": "model", "parts": []map[string]string{{"text": m.Content}}})
		default:
			contents = append(contents, map[string]any{"role": "user", "parts": []map[string]string{{"text": m.Content}}})
		}
	}
	body := map[string]any{"contents": contents}
	if len(system) > 0 {
		body["systemInstruction"] = map[string]any{"parts": []map[string]string{{"text": strings.Join(system, "\n\n")}}}
	}
	gen := map[string]any{}
	p := req.Params
	if p.Temperature != nil && !omit[ParamTemperature] {
		gen["temperature"] = *p.Temperature
	}
	if p.TopP != nil && !omit[ParamTopP] {
		gen["topP"] = *p.TopP
	}
	if p.MaxTokens != nil && !omit[ParamMaxTokens] {
		gen["maxOutputTokens"] = *p.MaxTokens
	}
	if len(p.Stop) > 0 && !omit[ParamStop] {
		gen["stopSequences"] = p.Stop
	}
	if p.Thinking != nil && !omit[ParamThinking] {
		gen["thinkingConfig"] = map[string]any{"thinkingBudget": *p.Thinking}
	}
	if len(req.Schema) > 0 && !omit[ParamSchema] {
		gen["responseMimeType"] = "application/json"
		gen["responseSchema"] = json.RawMessage(req.Schema)
	}
	if len(gen) > 0 {
		body["generationConfig"] = gen
	}
	u := join(ep.Base, "/v1beta/models/"+url.PathEscape(req.Model)+":streamGenerateContent")
	u.RawQuery = "alt=sse"
	resp, err := post(ctx, ep, u, g.headers(ep), body)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out Response
	var text answer
	finished := false
	err = sseEvents(resp.Body, func(_ string, data []byte) error {
		var chunk struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text    string `json:"text"`
						Thought bool   `json:"thought"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			UsageMetadata struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
				ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
			} `json:"usageMetadata"`
			ModelVersion string `json:"modelVersion"`
			Error        *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &chunk); err != nil {
			return &APIError{Streamed: true, Message: "a chunk is not JSON: " + err.Error()}
		}
		if chunk.Error != nil {
			return &APIError{Status: chunk.Error.Code, Streamed: true, Message: bound(chunk.Error.Message)}
		}
		if chunk.ModelVersion != "" {
			out.Model = chunk.ModelVersion
		}
		if u := chunk.UsageMetadata; u.PromptTokenCount > 0 || u.CandidatesTokenCount > 0 {
			out.InputTokens, out.OutputTokens, out.Reported = u.PromptTokenCount, u.CandidatesTokenCount+u.ThoughtsTokenCount, true
		}
		for _, c := range chunk.Candidates {
			if c.FinishReason != "" {
				out.StopReason, finished = c.FinishReason, true
			}
			for _, part := range c.Content.Parts {
				if part.Thought {
					continue
				}
				if err := text.add(part.Text); err != nil {
					return err
				}
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

func (g google) Models(ctx context.Context, ep Endpoint) ([]string, error) {
	resp, err := get(ctx, ep, join(ep.Base, "/v1beta/models"), g.headers(ep))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var list struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := decodeBounded(resp, &list); err != nil {
		return nil, err
	}
	var out []string
	for _, m := range list.Models {
		out = append(out, strings.TrimPrefix(m.Name, "models/"))
	}
	return out, nil
}
