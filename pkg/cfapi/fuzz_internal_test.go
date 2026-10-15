// Fuzzing what an answer from the API, or anything answering in its place,
// can be.
package cfapi

import (
	"strings"
	"testing"
)

// FuzzDecode: any body, under any status, is either Cloudflare's
// successful envelope with a 200, or an error; an error never holds more
// than its bounded messages; nothing panics.
func FuzzDecode(f *testing.F) {
	for _, seed := range []string{
		`{"success":true,"errors":[],"result":{"id":"x"}}`, `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`,
		`{"success":true}`, `[]`, `null`, `{"success":"yes"}`, "\xff", `{"success":false,"errors":[` + strings.Repeat(`{"code":1,"message":"m"},`, 20) + `{"code":2,"message":"n"}]}`,
	} {
		f.Add(200, []byte(seed))
		f.Add(403, []byte(seed))
	}
	f.Fuzz(func(t *testing.T, status int, body []byte) {
		env, err := decode("/p", status, body)
		if err == nil {
			if status != 200 || !env.Success {
				t.Fatalf("status %d success %v accepted", status, env.Success)
			}
			return
		}
		if e, ok := err.(*Error); ok {
			if len(e.Messages) > 8 {
				t.Fatalf("%d messages", len(e.Messages))
			}
			for _, m := range e.Messages {
				if len(m) > 515 {
					t.Fatalf("a message of %d bytes", len(m))
				}
			}
		}
	})
}
