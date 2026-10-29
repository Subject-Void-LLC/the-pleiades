// Tests of how an engine's refusal reaches an error: Docker's and Podman's
// documented error JSON gives its message, bounded and quoted.
package containerengine

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGetJSON_SaysWhatTheEngineSaid serves the error bodies the Docker
// Engine API and Podman's libpod API document ({"message": ...}, Podman's
// with its cause and response code) and checks the engine's message, not
// the JSON around it, reaches the error.
func TestGetJSON_SaysWhatTheEngineSaid(t *testing.T) {
	for path, tc := range map[string][2]string{
		"/info":               {`{"message":"client version 1.52 is too new. Maximum supported API version is 1.47"}`, `answered 400 Bad Request: "client version 1.52 is too new. Maximum supported API version is 1.47"`},
		"/v5.0.0/libpod/info": {"{\n  \"cause\": \"permission denied\",\n  \"message\": \"unable to connect to Podman socket: permission denied\",\n  \"response\": 500\n}", `answered 400 Bad Request: "unable to connect to Podman socket: permission denied"`},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(tc[0]))
		}))
		dial := func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", strings.TrimPrefix(srv.URL, "http://"))
		}
		var out map[string]any
		err := getJSON(context.Background(), dial, path, &out)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tc[1]) {
			t.Errorf("GET %s = %v, want it to hold %s", path, err, tc[1])
		}
	}
}
