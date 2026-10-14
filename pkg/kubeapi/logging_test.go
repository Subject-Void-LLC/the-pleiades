// Tests that client-go's log goes only where LogTo sends it.
package kubeapi

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"k8s.io/klog/v2"
)

// TestLogTo_SendsClientGosLinesThereAndNowhereElse routes klog to a buffer,
// logs a line, and finds it there; then the default (discard) again, so a
// line logged afterwards reaches neither the buffer nor standard error.
func TestLogTo_SendsClientGosLinesThereAndNowhereElse(t *testing.T) {
	var buf bytes.Buffer
	LogTo(&buf)
	klog.Info("a client-go line")
	klog.Flush()
	if !strings.Contains(buf.String(), "a client-go line") {
		t.Fatalf("the routed log holds %q", buf.String())
	}

	LogTo(io.Discard)
	t.Cleanup(func() { LogTo(io.Discard) })
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	klog.Info("a line after the default is restored")
	klog.Flush()
	os.Stderr = saved
	_ = w.Close()
	leaked, _ := io.ReadAll(r)
	if bytes.Contains(leaked, []byte("after the default")) || strings.Contains(buf.String(), "after the default") {
		t.Errorf("a line logged with the default in place reached standard error or the old writer")
	}
}
