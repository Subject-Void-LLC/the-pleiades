// Tests for each kind against a server speaking its provider's format.
package aiprovider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
)

// TestAnthropic_StreamsAnAnswerAndAStructuredOne: the key goes in
// x-api-key with the API version, system messages become the system
// field, max_tokens is always sent, a thinking budget raises it, and a
// schema asks through a forced tool whose streamed input is the answer.
func TestAnthropic_StreamsAnAnswerAndAStructuredOne(t *testing.T) {
	srv := newServer(t, func(n int, w http.ResponseWriter, s seen) {
		if _, structured := s.Body["tools"]; structured {
			stream(w,
				`event: message_start`+"\n"+`data: {"type":"message_start","message":{"model":"claude-x","usage":{"input_tokens":12}}}`,
				`event: content_block_delta`+"\n"+`data: {"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"label\":"}}`,
				`event: content_block_delta`+"\n"+`data: {"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"\"bug\"}"}}`,
				`event: message_delta`+"\n"+`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
				`event: message_stop`+"\n"+`data: {"type":"message_stop"}`)
			return
		}
		stream(w,
			`event: message_start`+"\n"+`data: {"type":"message_start","message":{"model":"claude-x","usage":{"input_tokens":20}}}`,
			`event: content_block_delta`+"\n"+`data: {"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"hmm"}}`,
			`event: content_block_delta`+"\n"+`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}`,
			`event: content_block_delta`+"\n"+`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":", world"}}`,
			`event: message_delta`+"\n"+`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
			`event: message_stop`+"\n"+`data: {"type":"message_stop"}`)
	})
	k, ep := kind(t, "anthropic"), srv.endpoint(t, "", "sk-ant-test")
	resp, err := k.Chat(context.Background(), ep, aiprovider.Request{
		Model:    "claude-x",
		Messages: []aiprovider.Message{{Role: "system", Content: "Be brief."}, {Role: "user", Content: "Hi"}},
		Params:   aiprovider.Params{Temperature: f64(0.2), Thinking: i(2048), Stop: []string{"END"}},
	}, nil)
	if err != nil || resp.Text != "Hello, world" || resp.InputTokens != 20 || resp.OutputTokens != 5 || resp.StopReason != "end_turn" {
		t.Fatalf("Chat = %+v, %v", resp, err)
	}
	r := srv.requests()[0]
	if r.Path != "/v1/messages" || r.Header.Get("x-api-key") != "sk-ant-test" || r.Header.Get("anthropic-version") == "" || r.Header.Get("Authorization") != "" {
		t.Errorf("request = %s %v", r.Path, r.Header)
	}
	if r.Body["system"] != "Be brief." || r.Body["stream"] != true || r.Body["max_tokens"].(float64) <= 2048 || r.Body["stop_sequences"] == nil {
		t.Errorf("body = %v", r.Body)
	}
	resp, err = k.Chat(context.Background(), ep, aiprovider.Request{Model: "claude-x", Messages: []aiprovider.Message{{Role: "user", Content: "Classify"}}, Schema: json.RawMessage(`{"type":"object"}`)}, nil)
	if err != nil || resp.Text != `{"label":"bug"}` {
		t.Fatalf("a structured answer = %+v, %v", resp, err)
	}
	if tc, _ := srv.requests()[1].Body["tool_choice"].(map[string]any); tc["name"] != "answer" {
		t.Errorf("tool_choice = %v", srv.requests()[1].Body["tool_choice"])
	}
}

// TestOpenAI_StreamsAndOmitsWhatItCannotSend: a bearer key, usage from the
// final chunk, [DONE] ends the stream, a schema becomes response_format,
// and thinking (which this API has no budget for) is not accepted.
func TestOpenAI_StreamsAndOmitsWhatItCannotSend(t *testing.T) {
	srv := newServer(t, func(n int, w http.ResponseWriter, s seen) {
		stream(w,
			`data: {"model":"gpt-x","choices":[{"delta":{"content":"4"}}]}`,
			`data: {"model":"gpt-x","choices":[{"delta":{"content":"2"},"finish_reason":"stop"}]}`,
			`data: {"model":"gpt-x","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":2}}`,
			`data: [DONE]`)
	})
	k := kind(t, "openai")
	if k.Accepts(aiprovider.ParamThinking) {
		t.Error("openai accepts a thinking budget")
	}
	resp, err := k.Chat(context.Background(), srv.endpoint(t, "/v1", "sk-test"), aiprovider.Request{
		Model: "gpt-x", Messages: []aiprovider.Message{{Role: "user", Content: "6*7?"}},
		Params: aiprovider.Params{MaxTokens: i(10)}, Schema: json.RawMessage(`{"type":"string"}`),
	}, nil)
	if err != nil || resp.Text != "42" || resp.InputTokens != 9 || resp.OutputTokens != 2 || resp.StopReason != "stop" {
		t.Fatalf("Chat = %+v, %v", resp, err)
	}
	r := srv.requests()[0]
	if r.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test" || r.Body["max_tokens"] != float64(10) || r.Body["response_format"] == nil {
		t.Errorf("request = %s %v %v", r.Path, r.Header, r.Body)
	}
}

// TestGoogle_KeepsTheKeyOutOfTheURL: the key goes in x-goog-api-key, never
// the query; system text becomes systemInstruction, an assistant turn the
// model role; thought parts are not the answer but count as output.
func TestGoogle_KeepsTheKeyOutOfTheURL(t *testing.T) {
	srv := newServer(t, func(n int, w http.ResponseWriter, s seen) {
		stream(w,
			`data: {"candidates":[{"content":{"parts":[{"text":"thinking...","thought":true},{"text":"Paris"}]}}],"modelVersion":"gemini-x"}`,
			`data: {"candidates":[{"content":{"parts":[{"text":"."}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":2,"thoughtsTokenCount":30}}`)
	})
	resp, err := kind(t, "google").Chat(context.Background(), srv.endpoint(t, "", "AIza-test"), aiprovider.Request{
		Model:    "gemini-x",
		Messages: []aiprovider.Message{{Role: "system", Content: "Answer in one word."}, {Role: "user", Content: "Capital of France?"}, {Role: "assistant", Content: "Paris"}, {Role: "user", Content: "Again?"}},
		Params:   aiprovider.Params{TopP: f64(0.9), Thinking: i(128)},
	}, nil)
	if err != nil || resp.Text != "Paris." || resp.InputTokens != 8 || resp.OutputTokens != 32 || resp.Model != "gemini-x" {
		t.Fatalf("Chat = %+v, %v", resp, err)
	}
	r := srv.requests()[0]
	if r.Path != "/v1beta/models/gemini-x:streamGenerateContent" || r.Query != "alt=sse" || strings.Contains(r.Query, "key") || r.Header.Get("x-goog-api-key") != "AIza-test" {
		t.Errorf("request = %s?%s %v", r.Path, r.Query, r.Header)
	}
	contents := r.Body["contents"].([]any)
	if len(contents) != 3 || contents[1].(map[string]any)["role"] != "model" || r.Body["systemInstruction"] == nil {
		t.Errorf("contents = %v", r.Body)
	}
}

// TestOllama_ChatAndModels: NDJSON with counts on the last line, options
// for sampling, format for a schema; and the model management a host
// has: list, resident, load (kept), unload, pull to success, delete.
func TestOllama_ChatAndModels(t *testing.T) {
	srv := newServer(t, func(n int, w http.ResponseWriter, s seen) {
		switch s.Path {
		case "/api/chat":
			fmt.Fprintln(w, `{"model":"qwen3:8b","message":{"content":"Hel"},"done":false}`)
			fmt.Fprintln(w, `{"model":"qwen3:8b","message":{"content":"lo"},"done":false}`)
			fmt.Fprintln(w, `{"model":"qwen3:8b","message":{"content":""},"done":true,"done_reason":"stop","prompt_eval_count":11,"eval_count":2}`)
		case "/api/tags":
			fmt.Fprint(w, `{"models":[{"name":"qwen3:8b","size":5000,"digest":"abc"},{"name":"llama3:8b","size":4000}]}`)
		case "/api/ps":
			fmt.Fprint(w, `{"models":[{"name":"llama3:8b"}]}`)
		case "/api/pull":
			fmt.Fprintln(w, `{"status":"pulling manifest"}`)
			fmt.Fprintln(w, `{"status":"success"}`)
		default:
			fmt.Fprint(w, `{}`)
		}
	})
	ep := srv.endpoint(t, "", "")
	resp, err := kind(t, "ollama").Chat(context.Background(), ep, aiprovider.Request{
		Model: "qwen3:8b", Messages: []aiprovider.Message{{Role: "user", Content: "hi"}},
		Params: aiprovider.Params{Temperature: f64(0), MaxTokens: i(64)}, Schema: json.RawMessage(`{"type":"object"}`),
	}, nil)
	if err != nil || resp.Text != "Hello" || resp.InputTokens != 11 || resp.OutputTokens != 2 {
		t.Fatalf("Chat = %+v, %v", resp, err)
	}
	body := srv.requests()[0].Body
	if opts := body["options"].(map[string]any); opts["num_predict"] != float64(64) || opts["temperature"] != float64(0) || body["format"] == nil {
		t.Errorf("body = %v", body)
	}
	models, err := aiprovider.OllamaModels(context.Background(), ep)
	if err != nil || len(models) != 2 || models[0].Digest != "abc" {
		t.Errorf("models = %+v, %v", models, err)
	}
	resident, err := aiprovider.OllamaResident(context.Background(), ep)
	if err != nil || len(resident) != 1 || resident[0].Name != "llama3:8b" {
		t.Errorf("resident = %+v, %v", resident, err)
	}
	for _, step := range []func() error{
		func() error { return aiprovider.OllamaLoad(context.Background(), ep, "qwen3:8b") },
		func() error { return aiprovider.OllamaUnload(context.Background(), ep, "llama3:8b") },
		func() error { return aiprovider.OllamaPull(context.Background(), ep, "qwen3:8b") },
		func() error { return aiprovider.OllamaDelete(context.Background(), ep, "qwen3:8b") },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	reqs := srv.requests()
	if reqs[3].Body["keep_alive"] != float64(-1) || reqs[4].Body["keep_alive"] != float64(0) || reqs[6].Method != "DELETE" {
		t.Errorf("model management requests = %+v", reqs[3:])
	}
}

// TestKinds_UsageIsReportedOnlyWhenTheProviderSaidSo: a stream carrying no
// token counts is a call whose usage is unreported, not one of zero tokens;
// one carrying them is reported.
func TestKinds_UsageIsReportedOnlyWhenTheProviderSaidSo(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		path     string
		silent   []string
		reported []string
	}{
		{"openai", "/v1",
			[]string{`data: {"model":"m","choices":[{"delta":{"content":"hi"}}]}`, `data: [DONE]`},
			[]string{`data: {"model":"m","choices":[{"delta":{"content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`, `data: [DONE]`}},
		{"anthropic", "/v1",
			[]string{`data: {"type":"message_start","message":{"model":"m"}}`, `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, `data: {"type":"message_stop"}`},
			[]string{`data: {"type":"message_start","message":{"model":"m","usage":{"input_tokens":1}}}`, `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`, `data: {"type":"message_stop"}`}},
	} {
		for label, events := range map[string][]string{"silent": tc.silent, "reported": tc.reported} {
			srv := newServer(t, func(_ int, w http.ResponseWriter, _ seen) { stream(w, events...) })
			req := aiprovider.Request{Model: "m", Messages: []aiprovider.Message{{Role: "user", Content: "x"}}}
			_, usage, err := aiprovider.Call(context.Background(), kind(t, tc.kind), srv.endpoint(t, tc.path, "k"), req, nil, aiprovider.CallOptions{})
			if err != nil {
				t.Fatalf("%s %s: %v", tc.kind, label, err)
			}
			if want := label == "reported"; usage.Reported != want {
				t.Errorf("%s %s: Reported = %v, want %v", tc.kind, label, usage.Reported, want)
			}
		}
	}
}

// TestKinds_TextTheProviderSendsIsBoundedAndEscaped: a streamed error, the
// model it names and why it stopped reach the caller cut to a size and with
// no character that acts on a terminal.
func TestKinds_TextTheProviderSendsIsBoundedAndEscaped(t *testing.T) {
	long := strings.Repeat("e", 100000)
	srv := newServer(t, func(_ int, w http.ResponseWriter, _ seen) {
		stream(w, `data: {"error":{"message":"`+long+`\u001b]0;pwned\u0007"}}`)
	})
	req := aiprovider.Request{Model: "m", Messages: []aiprovider.Message{{Role: "user", Content: "x"}}}
	_, _, err := aiprovider.Call(context.Background(), kind(t, "openai"), srv.endpoint(t, "/v1", "k"), req, nil, aiprovider.CallOptions{})
	if err == nil || len(err.Error()) > 1024 || strings.ContainsRune(err.Error(), 0x1b) {
		t.Errorf("a streamed error = %d bytes, %q", len(err.Error()), err)
	}

	srv = newServer(t, func(_ int, w http.ResponseWriter, _ seen) {
		stream(w, `data: {"model":"`+long+`\u001b[31m","choices":[{"delta":{"content":"hi"},"finish_reason":"stop\u001b[0m"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`, `data: [DONE]`)
	})
	resp, usage, err := aiprovider.Call(context.Background(), kind(t, "openai"), srv.endpoint(t, "/v1", "k"), req, nil, aiprovider.CallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{resp.Model, usage.Model, resp.StopReason} {
		if len(s) > 256 || strings.ContainsRune(s, 0x1b) {
			t.Errorf("provider text %d bytes, %q", len(s), s)
		}
	}
}

// TestKinds_AStreamThatEndsUnfinishedIsAnError: a stream that ends cleanly
// without the provider's closing marker holds part of an answer, which is
// refused rather than returned as the whole of it.
func TestKinds_AStreamThatEndsUnfinishedIsAnError(t *testing.T) {
	for name, events := range map[string][]string{
		"openai":    {`data: {"model":"m","choices":[{"delta":{"content":"half a sen"}}]}`},
		"anthropic": {`data: {"type":"message_start","message":{"model":"m","usage":{"input_tokens":1}}}`, `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"half"}}`},
		"google":    {`data: {"candidates":[{"content":{"parts":[{"text":"half"}]}}]}`},
	} {
		srv := newServer(t, func(_ int, w http.ResponseWriter, _ seen) { stream(w, events...) })
		req := aiprovider.Request{Model: "m", Messages: []aiprovider.Message{{Role: "user", Content: "x"}}}
		resp, _, err := aiprovider.Call(context.Background(), kind(t, name), srv.endpoint(t, "/v1", "k"), req, nil, aiprovider.CallOptions{})
		if err == nil || !strings.Contains(err.Error(), "before the provider finished") || resp.Text != "" {
			t.Errorf("%s: a cut-off stream = %q, %v", name, resp.Text, err)
		}
	}
	srv := newServer(t, func(_ int, w http.ResponseWriter, _ seen) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"model":"m","message":{"content":"half"},"done":false}` + "\n"))
	})
	req := aiprovider.Request{Model: "m", Messages: []aiprovider.Message{{Role: "user", Content: "x"}}}
	if _, _, err := aiprovider.Call(context.Background(), kind(t, "ollama"), srv.endpoint(t, "", "k"), req, nil, aiprovider.CallOptions{}); err == nil || !strings.Contains(err.Error(), "before the provider finished") {
		t.Errorf("ollama: a cut-off stream = %v", err)
	}
}
