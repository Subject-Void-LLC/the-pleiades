// Tests that the CLI routes client-go's own log through its masking.
package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"k8s.io/klog/v2"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/kubeapi"
)

// TestRun_ClientGosLogIsMasked: run sends client-go's log lines to standard
// error through the CLI's masking (kubeapi.LogTo, redact.Shared). A line
// shaped like the leak client-go's logging could make, an Authorization
// header with a bearer token, reaches standard error with the line kept and
// the token masked. A routing that bypassed the mask, or left klog writing
// to standard error itself, would print it.
func TestRun_ClientGosLogIsMasked(t *testing.T) {
	const token = "eyKlogSentinel7Qw2ZrT9mXv4LpB8nC3dF6hJ"
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = saved; kubeapi.LogTo(io.Discard, nil) })

	run(nil) // no command: prints usage, but routes client-go's log first
	klog.Infof("request headers: Authorization: Bearer %s", token)
	klog.Flush()
	os.Stderr = saved
	_ = w.Close()
	out, _ := io.ReadAll(r)

	if !strings.Contains(string(out), "request headers") {
		t.Fatalf("client-go's line did not reach standard error, so the test proves nothing:\n%s", out)
	}
	if strings.Contains(string(out), token) {
		t.Errorf("client-go's line reached standard error with the token unmasked:\n%s", out)
	}
}

// TestRun_ClientGosLogMasksAClusterTokenByValue: a token no shape rule
// knows, quoted bare in a client-go line, passes the CLI's masking until a
// cluster configuration holds it, and is masked from then on, because run
// gives pkg/kubeapi the masker's by-value set along with its writer.
func TestRun_ClientGosLogMasksAClusterTokenByValue(t *testing.T) {
	const token = "plainClusterTokenSentinel4kq9zv2m"
	t.Cleanup(func() { redact.Shared().Literals().Forget(token) })
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = saved; kubeapi.LogTo(io.Discard, nil) })

	run(nil) // no command: prints usage, but routes client-go's log first
	klog.Infof("before the session: %s", token)
	if _, err := kubeapi.Config(kubeapi.Endpoint{Server: "https://cluster.example:6443"}, kubeapi.Identity{Token: token}, kubeapi.Limits{}); err != nil {
		t.Fatal(err)
	}
	klog.Infof("after the session: %s", token)
	klog.Flush()
	os.Stderr = saved
	_ = w.Close()
	out, _ := io.ReadAll(r)

	before, after, found := strings.Cut(string(out), "after the session")
	if !found || !strings.Contains(before, "before the session: "+token) {
		t.Fatalf("the shape rules masked the bare token, or a line is missing, so the test proves nothing:\n%s", out)
	}
	if strings.Contains(after, token) {
		t.Errorf("a line logged once a configuration held the token printed it:\n%s", out)
	}
}
