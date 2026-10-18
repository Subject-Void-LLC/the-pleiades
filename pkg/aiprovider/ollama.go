// Package aiprovider: the ollama kind, Ollama's API (POST /api/chat,
// streamed as newline-delimited JSON), and the model management only an
// Ollama host has.
package aiprovider

import (
	"context"
	"encoding/json"
	"fmt"
)

type ollama struct{}

func init() { mustRegister(ollama{}) }

func (ollama) Name() string        { return "ollama" }
func (ollama) NeedsKey() bool      { return false }
func (ollama) Accepts(string) bool { return true }

func (ollama) WireName(p string) string {
	switch p {
	case ParamMaxTokens:
		return "num_predict"
	case ParamThinking:
		return "think"
	case ParamSchema:
		return "format"
	}
	return p
}

// headers send a key as a bearer token when there is one (an Ollama
// behind an authenticating proxy).
func (ollama) headers(ep Endpoint) map[string]string {
	if ep.Key == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + ep.Key}
}

func (o ollama) Chat(ctx context.Context, ep Endpoint, req Request, omit map[string]bool) (Response, error) {
	options := map[string]any{}
	p := req.Params
	if p.Temperature != nil && !omit[ParamTemperature] {
		options["temperature"] = *p.Temperature
	}
	if p.TopP != nil && !omit[ParamTopP] {
		options["top_p"] = *p.TopP
	}
	if p.MaxTokens != nil && !omit[ParamMaxTokens] {
		options["num_predict"] = *p.MaxTokens
	}
	if len(p.Stop) > 0 && !omit[ParamStop] {
		options["stop"] = p.Stop
	}
	body := map[string]any{"model": req.Model, "messages": req.Messages, "stream": true}
	if len(options) > 0 {
		body["options"] = options
	}
	if p.Thinking != nil && !omit[ParamThinking] {
		// Ollama's thinking is on or off; a budget of zero turns it off.
		body["think"] = *p.Thinking > 0
	}
	if len(req.Schema) > 0 && !omit[ParamSchema] {
		body["format"] = json.RawMessage(req.Schema)
	}
	resp, err := post(ctx, ep, join(ep.Base, "/api/chat"), o.headers(ep), body)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out Response
	var text answer
	finished := false
	err = ndjson(resp.Body, func(line []byte) error {
		var chunk struct {
			Model   string `json:"model"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done            bool   `json:"done"`
			DoneReason      string `json:"done_reason"`
			PromptEvalCount int    `json:"prompt_eval_count"`
			EvalCount       int    `json:"eval_count"`
			Error           string `json:"error"`
		}
		if err := json.Unmarshal(line, &chunk); err != nil {
			return &APIError{Streamed: true, Message: "a line is not JSON: " + err.Error()}
		}
		if chunk.Error != "" {
			return &APIError{Status: 500, Streamed: true, Message: bound(chunk.Error)}
		}
		out.Model = chunk.Model
		if chunk.Done {
			finished = true
			out.InputTokens, out.OutputTokens, out.StopReason, out.Reported = chunk.PromptEvalCount, chunk.EvalCount, chunk.DoneReason, true
		}
		return text.add(chunk.Message.Content)
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

func (o ollama) Models(ctx context.Context, ep Endpoint) ([]string, error) {
	models, err := OllamaModels(ctx, ep)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.Name
	}
	return out, nil
}

// OllamaModel is a model an Ollama host has.
type OllamaModel struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Digest identifies the model's content.
	Digest string `json:"digest"`
}

// OllamaModels lists the models an Ollama host has (GET /api/tags).
func OllamaModels(ctx context.Context, ep Endpoint) ([]OllamaModel, error) {
	return ollamaList(ctx, ep, "/api/tags")
}

// OllamaResident lists the models an Ollama host has loaded in memory now
// (GET /api/ps).
func OllamaResident(ctx context.Context, ep Endpoint) ([]OllamaModel, error) {
	return ollamaList(ctx, ep, "/api/ps")
}

func ollamaList(ctx context.Context, ep Endpoint, path string) ([]OllamaModel, error) {
	resp, err := get(ctx, ep, join(ep.Base, path), ollama{}.headers(ep))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var list struct {
		Models []OllamaModel `json:"models"`
	}
	if err := decodeBounded(resp, &list); err != nil {
		return nil, err
	}
	return list.Models, nil
}

// OllamaLoad loads model into memory and keeps it there (an empty
// generate with keep_alive -1).
func OllamaLoad(ctx context.Context, ep Endpoint, model string) error {
	return ollamaGenerate(ctx, ep, model, -1)
}

// OllamaUnload unloads model from memory (an empty generate with
// keep_alive 0).
func OllamaUnload(ctx context.Context, ep Endpoint, model string) error {
	return ollamaGenerate(ctx, ep, model, 0)
}

func ollamaGenerate(ctx context.Context, ep Endpoint, model string, keepAlive int) error {
	resp, err := post(ctx, ep, join(ep.Base, "/api/generate"), ollama{}.headers(ep), map[string]any{"model": model, "keep_alive": keepAlive, "stream": false})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// OllamaPull pulls model, streaming Ollama's progress until it says
// success.
func OllamaPull(ctx context.Context, ep Endpoint, model string) error {
	resp, err := post(ctx, ep, join(ep.Base, "/api/pull"), ollama{}.headers(ep), map[string]any{"model": model, "stream": true})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	success := false
	err = ndjson(resp.Body, func(line []byte) error {
		var st struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(line, &st); err != nil {
			return &APIError{Streamed: true, Message: "a line is not JSON: " + err.Error()}
		}
		if st.Error != "" {
			return &APIError{Status: 500, Streamed: true, Message: bound(st.Error)}
		}
		success = success || st.Status == "success"
		return nil
	})
	if err != nil {
		return err
	}
	if !success {
		return fmt.Errorf("Ollama ended the pull of %s without saying it succeeded", model)
	}
	return nil
}

// OllamaDelete deletes model from the host (DELETE /api/delete).
func OllamaDelete(ctx context.Context, ep Endpoint, model string) error {
	data, _ := json.Marshal(map[string]string{"model": model})
	req, err := newRequest(ctx, "DELETE", join(ep.Base, "/api/delete"), data, ollama{}.headers(ep))
	if err != nil {
		return err
	}
	resp, err := send(ep, req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
