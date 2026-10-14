// client-go's own logging: discarded unless a composition root routes it,
// and then only through that root's redaction.
package kubeapi

import (
	"io"

	"k8s.io/klog/v2"
)

func init() {
	// Discarded until a composition root says otherwise. client-go logs
	// through klog, and at high verbosity klog writes request and response
	// bodies, which carry tokens and Secret data; the safe default for a
	// library is to write nothing at all.
	LogTo(io.Discard)
}

// LogTo sends client-go's log lines to w, and nowhere else. A composition
// root passes its redacting writer (internal/redact's Masker.Writer around
// standard error), which pkg/kubeapi cannot import itself. Nothing here
// raises klog's verbosity, so request bodies are never logged regardless.
func LogTo(w io.Writer) {
	klog.LogToStderr(false)
	klog.SetOutput(w)
}
