// Tests for how a WS-Man fault is read: the routine "no output yet" fault
// a Receive answers with must be told apart from a real failure, and a real
// failure must read as the fault's own sentence. The fault in testdata was
// captured from a real Windows host through the certificate transport.
package winrmexec

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// timedOutFaultBody is the fault a real host sent for a Receive that
// waited out OperationTimeout with nothing to return.
func timedOutFaultBody(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/timedout-fault.xml")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestReceive_WaitsThroughATimedOutFaultOverTheCertificateTransport runs
// the real receive loop over the real certificate transport against a
// server playing the WinRM service, whose first Receive answers with the
// captured fault, as a host does while a command writes nothing. The
// command must go on to finish; before the fault was kept whole, this
// ended the command with a 500.
func TestReceive_WaitsThroughATimedOutFaultOverTheCertificateTransport(t *testing.T) {
	fault := timedOutFaultBody(t)
	var mu sync.Mutex
	receives := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/soap+xml;charset=UTF-8")
		switch actionOf(string(body)) {
		case "Create":
			_, _ = w.Write([]byte(shellReply))
		case "Command":
			_, _ = w.Write([]byte(cmdReply))
		case "Receive":
			mu.Lock()
			receives++
			first := receives == 1
			mu.Unlock()
			if first {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(fault))
				return
			}
			_, _ = w.Write([]byte(output("done", "", 0)))
		default:
			_, _ = w.Write([]byte(okReply))
		}
	}))
	x := &exchange{transport: plainTransportTo(t, server), url: "http://h:5986/wsman"}
	res, err := x.run(context.Background(), "prog", "", shellSpec{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Stdout != "done" || receives != 2 {
		t.Errorf("result %+v after %d receives, want done after 2", res, receives)
	}
}

func TestIsNoOutputYet(t *testing.T) {
	fault := timedOutFaultBody(t)
	// The same fault from a host that answers in German: detected by its
	// code, not its sentence.
	german := strings.ReplaceAll(fault, "The WS-Management service cannot complete the operation within the time specified in OperationTimeout.",
		"Der WS-Verwaltungsdienst kann den Vorgang nicht in der unter OperationTimeout angegebenen Zeit abschliessen.")
	german = strings.ReplaceAll(german, "OperationTimeout", "Vorgangszeitlimit")
	accessDenied := strings.NewReplacer("w:TimedOut", "w:AccessDenied", `Code="2150858793"`, `Code="5"`).Replace(fault)
	for name, tt := range map[string]struct {
		err  error
		want bool
	}{
		"the certificate transport's fault":     {&httpFault{url: "u", status: 500, body: fault}, true},
		"a translated fault":                    {&httpFault{url: "u", status: 500, body: german}, true},
		"the library's error, quoting the body": {errors.New("http error 500: " + fault), true},
		"a different fault":                     {&httpFault{url: "u", status: 500, body: accessDenied}, false},
		"not a fault":                           {errors.New("connection reset"), false},
	} {
		if got := isNoOutputYet(tt.err); got != tt.want {
			t.Errorf("%s: isNoOutputYet = %v, want %v", name, got, tt.want)
		}
	}
}

// TestHTTPFault_ReadsAsItsSentence proves a fault reaches an operator as
// the sentence inside it rather than as the envelope around it.
func TestHTTPFault_ReadsAsItsSentence(t *testing.T) {
	err := &httpFault{url: "https://h:5986/wsman", status: 500, body: timedOutFaultBody(t)}
	want := "winrm: https://h:5986/wsman answered 500: The WS-Management service cannot complete the operation within the time specified in OperationTimeout."
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	bare := &httpFault{url: "u", status: 503, body: "  no   fault here  "}
	if bare.Error() != "winrm: u answered 503: no fault here" {
		t.Errorf("Error() without a fault = %q", bare.Error())
	}
}
