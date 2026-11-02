// Tests of the fact-key list a context's EmitFact keeps (facts.go).
package sdk_test

import (
	"encoding/json"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func TestFactKeys_RoundTripInProcessAndAcrossJSON(t *testing.T) {
	var keys []any
	for _, k := range []string{"a", "b", "a"} {
		keys = sdk.NoteFactKey(keys, k)
	}
	got, err := sdk.ParseFactKeys(keys)
	if err != nil || len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("in process: %v, %v", got, err)
	}
	raw, _ := json.Marshal(keys)
	var decoded any
	_ = json.Unmarshal(raw, &decoded)
	if got, err := sdk.ParseFactKeys(decoded); err != nil || len(got) != 2 {
		t.Errorf("across JSON: %v, %v", got, err)
	}
	for name, bad := range map[string]any{"not a list": "a", "a number": []any{1}, "empty": []any{""}} {
		if _, err := sdk.ParseFactKeys(bad); err == nil {
			t.Errorf("%s parsed", name)
		}
	}
}
