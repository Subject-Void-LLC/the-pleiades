// Tests of farside against error answers in the shapes real systems send:
// each vendor's documented error body, and captures already in this
// repository's testdata.
package farside

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestText_FindsTheReasonInEachVendorsShape feeds Text one error answer per
// documented format and checks that the reason, and only the reason, comes
// back quoted.
func TestText_FindsTheReasonInEachVendorsShape(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"cisco cml, indented json", "{\n  \"code\": 400,\n  \"description\": \"Not enough resources to start node CORE2\"\n}",
			`"Not enough resources to start node CORE2"`},
		{"rfc 9457 problem details", `{"type":"about:blank","title":"Conflict","status":409,"detail":"lab is locked"}`,
			`"lab is locked"`},
		{"kubernetes status", `{"kind":"Status","apiVersion":"v1","metadata":{},"status":"Failure","message":"pods \"web\" is forbidden: User \"ci\" cannot get resource \"pods\"","reason":"Forbidden","code":403}`,
			`"pods \"web\" is forbidden: User \"ci\" cannot get resource \"pods\""`},
		{"docker engine", `{"message":"No such container: web"}`, `"No such container: web"`},
		{"hashicorp vault", `{"errors":["permission denied"]}`, `"permission denied"`},
		{"cloudflare envelope", `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}],"messages":[],"result":null}`,
			`"Authentication error"`},
		{"catalyst center wrapper", `{"response":{"errorCode":"NCND01003","message":"Invalid input request","detail":"Invalid UUID: abc"},"version":"1.0"}`,
			`"Invalid UUID: abc"`},
		{"oauth 2.0 token error", `{"error":"invalid_grant","error_description":"The refresh token has expired"}`,
			`"The refresh token has expired"`},
		{"fastapi validation list", `{"detail":[{"loc":["query","limit"],"msg":"value is not a valid integer","type":"type_error.integer"}]}`,
			`"value is not a valid integer"`},
		{"an error object", `{"error":{"code":"rate_limited","message":"slow down"}}`, `"slow down"`},
		{"aws style capitalized key", `{"__type":"AccessDeniedException","Message":"User is not authorized"}`, `"User is not authorized"`},
		{"json with no reason field is whole", "{\n  \"code\": 400\n}", `"{ \"code\": 400 }"`},
		{"netconf rpc-error", `<?xml version="1.0" encoding="UTF-8"?>
<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="1"><rpc-error>
<error-type>protocol</error-type>
<error-tag>invalid-value</error-tag>
<error-severity>error</error-severity>
<error-message xml:lang="en">Unsupported capability :candidate</error-message><error-info><bad-element>candidate</bad-element>
</error-info>
</rpc-error>
</rpc-reply>`, `"Unsupported capability :candidate"`},
		{"soap 1.1 fault", `<?xml version="1.0"?><soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><soap:Fault><faultcode>soap:Server</faultcode><faultstring>Server was unable to process request</faultstring></soap:Fault></soap:Body></soap:Envelope>`,
			`"Server was unable to process request"`},
		{"nginx error page", "<html>\r\n<head><title>502 Bad Gateway</title></head>\r\n<body>\r\n<center><h1>502 Bad Gateway</h1></center>\r\n<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n",
			`"502 Bad Gateway 502 Bad Gateway nginx"`},
		{"html script is not words", `<html><head><script>var x = 1;</script></head><body>Forbidden</body></html>`, `"Forbidden"`},
		{"plain text over lines", "plain text\nover two lines", `"plain text over two lines"`},
		{"markup-looking text", "<none>", `"<none>"`},
		{"empty", "", `""`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Text(tc.body); got != tc.want {
				t.Errorf("Text = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestText_ReadsARealWinRMFault reads the WS-Management fault Windows sent
// (pkg/winrmexec's capture), whose reason follows about 800 bytes of
// envelope header a first-bytes cut never reaches.
func TestText_ReadsARealWinRMFault(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "winrmexec", "testdata", "timedout-fault.xml"))
	if err != nil {
		t.Fatalf("reading the capture: %v", err)
	}
	want := `"The WS-Management service cannot complete the operation within the time specified in OperationTimeout."`
	if got := Text(string(data)); got != want {
		t.Errorf("Text = %s, want %s", got, want)
	}
}

// TestText_IsBoundedAndShowsControlCharacters checks the three promises
// every caller relies on: a long answer is cut and marked, a cut never
// splits a character, and a terminal escape is shown, never sent.
func TestText_IsBoundedAndShowsControlCharacters(t *testing.T) {
	long := `{"description": "` + strings.Repeat("a", 400) + `"}`
	if got := Text(long); !strings.HasSuffix(got, `..."`) || len(got) > MaxBytes+10 {
		t.Errorf("a long reason was not bounded: %d bytes", len(got))
	}
	wide := strings.Repeat("é", 200) + "z" // two bytes each, so a byte cut would land inside one
	if got := Text(wide); !utf8.ValidString(got) || len(got) > MaxBytes+5 || !strings.HasSuffix(got, `é..."`) {
		t.Errorf("the cut split a character or overran: %q", got)
	}
	// Each control byte quotes as four bytes; the bound holds the quoted
	// text, so a body of them is no longer than plain text, and no escape
	// is cut in half.
	if got := Text(strings.Repeat("\x01", 400)); len(got) > MaxBytes+5 || !strings.HasSuffix(got, `\x01..."`) {
		t.Errorf("a body of control bytes was not bounded as quoted: %d bytes, %q", len(got), got[len(got)-12:])
	}
	if got := Text("{\"description\": \"bad\\u001b[2Jthing\"}"); strings.ContainsRune(got, 0x1b) || !strings.Contains(got, `\x1b`) {
		t.Errorf("a control character was not shown escaped: %s", got)
	}
}

// TestMasked_MasksBeforeTheCut proves the mask sees the whole reason: a
// secret placed across the bound is replaced whole, not left half visible.
func TestMasked_MasksBeforeTheCut(t *testing.T) {
	secret := "hunter2hunter2"
	body := strings.Repeat("x", MaxBytes-5) + " " + secret
	mask := func(s string) string { return strings.ReplaceAll(s, secret, "MASKED") }
	got := Masked(body, mask)
	if strings.Contains(got, "hunter") {
		t.Errorf("part of the secret survived the mask: %s", got)
	}
	if got := Masked("key "+secret, nil); !strings.Contains(got, secret) {
		t.Errorf("a nil mask changed the text: %s", got)
	}
}

// TestOutput_SaysWhenACommandWroteNothing covers the stderr form.
func TestOutput_SaysWhenACommandWroteNothing(t *testing.T) {
	for in, want := range map[string]string{
		"":                                 NoOutput,
		"  \n ":                            NoOutput,
		"Unit web.service not found.\n":    `"Unit web.service not found."`,
		"stat: cannot stat '/x'\nsecond\n": `"stat: cannot stat '/x' second"`,
	} {
		if got := Output(in); got != want {
			t.Errorf("Output(%q) = %s, want %s", in, got, want)
		}
	}
	if got := MaskedOutput("password is s3cret", func(s string) string { return strings.ReplaceAll(s, "s3cret", "*") }); got != `"password is *"` {
		t.Errorf("MaskedOutput = %s", got)
	}
}

// FuzzText holds Text to its contract on any input: it never panics, the
// result is always one quoted line, it never exceeds the bound by more than
// the cut mark and quotes, and a cut is always marked.
func FuzzText(f *testing.F) {
	for _, seed := range []string{"", "{", "[]", `{"errors":[[[["x"]]]]}`, "<a><b>", "<Message>x</Message>", "\x00\x1b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		got := Text(body)
		if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) || strings.ContainsAny(got, "\n\r") {
			t.Fatalf("Text(%q) = %s, not one quoted line", body, got)
		}
		if len(got) > MaxBytes+5 {
			t.Fatalf("Text(%q) is %d bytes, over the bound", body, len(got))
		}
		if len(strconv.Quote(Words(body))) > MaxBytes+2 && !strings.HasSuffix(got, `..."`) {
			t.Fatalf("Text(%q) was not marked as cut", body)
		}
	})
}
