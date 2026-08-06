package catalyst_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// pageDevices slices the captured device fixture according to the request's
// offset and limit, so the fake controller pages the way a real one does.
//
// A handler that ignored paging and always returned every device would make
// two real behaviors untestable: that EachDevice actually advances its
// offset, and that it stops on a short final page rather than looping
// forever. Catalyst Center's offset is one-based, which is the off-by-one
// this function exists to model faithfully rather than paper over.
func pageDevices(t *testing.T, fixture []byte, r *http.Request) []byte {
	t.Helper()

	var decoded struct {
		Response []json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(fixture, &decoded); err != nil {
		t.Fatalf("decoding device fixture: %v", err)
	}

	offset := intQuery(t, r, "offset", 1)
	limit := intQuery(t, r, "limit", len(decoded.Response))
	if offset < 1 {
		offset = 1
	}

	start := offset - 1
	if start > len(decoded.Response) {
		start = len(decoded.Response)
	}
	end := start + limit
	if end > len(decoded.Response) {
		end = len(decoded.Response)
	}

	page, err := json.Marshal(map[string]any{"response": decoded.Response[start:end]})
	if err != nil {
		t.Fatalf("encoding device page: %v", err)
	}
	return page
}

// intQuery reads an integer query parameter, falling back when it is absent
// or unparseable.
func intQuery(t *testing.T, r *http.Request, key string, fallback int) int {
	t.Helper()

	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("query parameter %s = %q is not an integer", key, raw)
	}
	return v
}
