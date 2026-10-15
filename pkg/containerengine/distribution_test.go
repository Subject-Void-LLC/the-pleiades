// Tests for Session.ResolveDigest against an engine answering its
// distribution route the way a real one does.
package containerengine

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// distributionSession opens a session whose engine answers the
// distribution route with body.
func distributionSession(t *testing.T, body string) (*Session, *string) {
	t.Helper()
	asked := new(string)
	engine := &pipeEngine{answer: func(req *http.Request) string {
		path := versionedPath.ReplaceAllString(req.URL.Path, "")
		if path == "/_ping" {
			return "HTTP/1.1 200 OK\r\nAPI-Version: 1.44\r\nContent-Length: 2\r\n\r\nOK"
		}
		*asked = path
		return jsonAnswer("200 OK", body)
	}}
	api, err := newAPI(guard(engine.dial, DistributionOps))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = api.Close() })
	return &Session{API: api}, asked
}

// TestResolveDigest_ReadsTheIndexAndItsPlatforms.
func TestResolveDigest_ReadsTheIndexAndItsPlatforms(t *testing.T) {
	digest := "sha256:" + strings.Repeat("ab", 32)
	s, asked := distributionSession(t, `{"Descriptor":{"mediaType":"application/vnd.oci.image.index.v1+json","digest":"`+digest+`","size":1234},
		"Platforms":[{"architecture":"amd64","os":"linux"},{"architecture":"arm64","os":"linux","variant":"v8"}]}`)
	res, err := s.ResolveDigest(context.Background(), "ghcr.io/team/app:2.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Digest != digest || res.MediaType != "application/vnd.oci.image.index.v1+json" || strings.Join(res.Platforms, ",") != "linux/amd64,linux/arm64/v8" {
		t.Errorf("Resolved = %+v", res)
	}
	if *asked != "/distribution/ghcr.io/team/app:2.1/json" {
		t.Errorf("asked %s", *asked)
	}
}

// TestResolveDigest_RefusesWhatItCannotWrite: an answer whose digest is not
// sha256 is refused, never handed on to be written into a file; a
// reference that is not one is refused before contact.
func TestResolveDigest_RefusesWhatItCannotWrite(t *testing.T) {
	s, _ := distributionSession(t, `{"Descriptor":{"digest":"sha256:../../etc"}}`)
	if _, err := s.ResolveDigest(context.Background(), "alpine:3.20", ""); err == nil || !strings.Contains(err.Error(), "not sha256") {
		t.Fatalf("a malformed digest = %v", err)
	}
	s, asked := distributionSession(t, `{}`)
	if _, err := s.ResolveDigest(context.Background(), "--privileged", ""); err == nil || *asked != "" {
		t.Fatalf("a hostile reference = %v, asked %q", err, *asked)
	}
}
