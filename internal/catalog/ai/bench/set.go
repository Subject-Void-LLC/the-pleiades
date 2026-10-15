// ai.bench.run's prompt set and the scorers its answers are judged by.
package bench

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/aitask"
)

// item is one prompt of the set: its id, what it sends, and its scorer.
type item struct {
	id       string
	messages []aiprovider.Message
	score    func(answer string) bool
}

// parseSet reads prompts: a list of {id, text | messages, expect}.
func parseSet(raw any) ([]item, error) {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 || len(list) > maxPrompts {
		return nil, fmt.Errorf("%s is a list of 1 to %d {id, text or messages, expect}", paramPrompts, maxPrompts)
	}
	seen := map[string]bool{}
	out := make([]item, 0, len(list))
	for i, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d] is not a mapping", paramPrompts, i)
		}
		for k := range m {
			if k != "id" && k != "text" && k != "messages" && k != "expect" {
				return nil, fmt.Errorf("%s[%d]: %s is not id, text, messages or expect", paramPrompts, i, k)
			}
		}
		id, _ := m["id"].(string)
		if !promptID.MatchString(id) || seen[id] {
			return nil, fmt.Errorf("%s[%d]: id %q is missing, not letters, digits, ., - and _, or used twice", paramPrompts, i, id)
		}
		seen[id] = true
		it := item{id: id}
		text, hasText := m["text"].(string)
		_, hasMessages := m["messages"]
		switch {
		case hasText == hasMessages:
			return nil, fmt.Errorf("prompt %s: give exactly one of text and messages", id)
		case hasText && text != "":
			it.messages = []aiprovider.Message{{Role: "user", Content: text}}
		case hasText:
			return nil, fmt.Errorf("prompt %s: text is empty", id)
		default:
			var err error
			if it.messages, err = aitask.ParseMessages(m["messages"]); err != nil {
				return nil, fmt.Errorf("prompt %s: %w", id, err)
			}
		}
		var err error
		if it.score, err = scorer(m["expect"]); err != nil {
			return nil, fmt.Errorf("prompt %s: expect: %w", id, err)
		}
		out = append(out, it)
	}
	return out, nil
}

// scorer reads expect: exactly one of exact, contains, regex (RE2) and
// json_field ({path, equals}).
func scorer(raw any) (func(string) bool, error) {
	m, ok := raw.(map[string]any)
	if !ok || len(m) != 1 {
		return nil, fmt.Errorf("it is one of exact, contains, regex and json_field")
	}
	for kind, v := range m {
		switch kind {
		case "exact", "contains", "regex":
			want, ok := v.(string)
			if !ok || want == "" {
				return nil, fmt.Errorf("%s is non-empty text", kind)
			}
			switch kind {
			case "exact":
				return func(a string) bool { return strings.TrimSpace(a) == want }, nil
			case "contains":
				return func(a string) bool { return strings.Contains(a, want) }, nil
			}
			if len(want) > maxRegexBytes {
				return nil, fmt.Errorf("regex is over %d bytes", maxRegexBytes)
			}
			re, err := regexp.Compile(want)
			if err != nil {
				return nil, fmt.Errorf("regex: %w", err)
			}
			return re.MatchString, nil
		case "json_field":
			spec, ok := v.(map[string]any)
			path, _ := spec["path"].(string)
			equals, hasEquals := spec["equals"]
			if !ok || len(spec) != 2 || path == "" || !hasEquals {
				return nil, fmt.Errorf("json_field is {path, equals}")
			}
			want, err := json.Marshal(equals)
			if err != nil {
				return nil, fmt.Errorf("json_field equals: %w", err)
			}
			return func(a string) bool { return jsonField(a, path, string(want)) }, nil
		}
		return nil, fmt.Errorf("%s is not exact, contains, regex or json_field", kind)
	}
	return nil, fmt.Errorf("it is empty")
}

// jsonField reports whether answer, read as JSON, holds want (as JSON) at
// path, a dotted list of object keys.
func jsonField(answer, path, want string) bool {
	var v any
	if json.Unmarshal([]byte(answer), &v) != nil {
		return false
	}
	for _, key := range strings.Split(path, ".") {
		obj, ok := v.(map[string]any)
		if !ok {
			return false
		}
		if v, ok = obj[key]; !ok {
			return false
		}
	}
	got, err := json.Marshal(v)
	return err == nil && string(got) == want
}
