// Tests of how an answer that is not Cloudflare's envelope reaches an error:
// in its own words, bounded and quoted, never as the status's name alone.
package cfapi

import (
	"strings"
	"testing"
)

// TestDecode_AnAnswerOutsideTheEnvelopeSaysWhy feeds decode the two answers
// that come from in front of the API rather than from it: Cloudflare's edge
// refusing a request with its plain "error code: 1020", and a gateway's HTML
// page. Each error carries those words; an envelope's own messages still
// win when it has them.
func TestDecode_AnAnswerOutsideTheEnvelopeSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, want string
	}{
		{403, "error code: 1020", `the token may not read /zones (403): "error code: 1020"`},
		{502, "<html>\r\n<head><title>502 Bad Gateway</title></head>\r\n<body>\r\n<center><h1>502 Bad Gateway</h1></center>\r\n<hr><center>cloudflare</center>\r\n</body>\r\n</html>\r\n",
			`reading /zones: status 502: "502 Bad Gateway 502 Bad Gateway cloudflare"`},
		{403, `{"success":false,"errors":[{"code":9109,"message":"Unauthorized to access requested resource"}],"messages":[],"result":null}`,
			`(403): 9109 Unauthorized to access requested resource`},
	} {
		_, err := decode("/zones", tc.status, []byte(tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("decode(%d, %q) = %v, want it to hold %s", tc.status, tc.body, err, tc.want)
		}
	}
	// An empty answer still names the status, as it did.
	if _, err := decode("/zones", 500, nil); err == nil || !strings.Contains(err.Error(), "Internal Server Error") {
		t.Errorf("an empty 500 = %v", err)
	}
}
