// client-go's own logging: discarded unless a composition root routes it,
// and then only through that root's redaction.
package kubeapi

import (
	"io"
	"sync"

	"k8s.io/klog/v2"
)

func init() {
	// Discarded until a composition root says otherwise. client-go logs
	// through klog, and at high verbosity klog writes request and response
	// bodies, which carry tokens and Secret data; the safe default for a
	// library is to write nothing at all.
	LogTo(io.Discard, nil)
}

// SecretSet is a log masker's by-value channel (internal/redact's Literals,
// which pkg/kubeapi cannot import): every value added is scrubbed from each
// later line, and a forgotten one no longer is.
type SecretSet interface {
	Add(values ...string) int
	Forget(values ...string)
}

// logSecrets is the set LogTo was last given, nil while client-go's log is
// discarded.
var logSecrets struct {
	sync.RWMutex
	set SecretSet
}

// LogTo sends client-go's log lines to w, and nowhere else, and has every
// secret a session holds from then on (a bearer token, a client key, the
// AWS key an EKS token is minted from, and each token minted) added to
// secrets, so w's masking scrubs it by value and not only when it is shaped
// like a secret. A composition root passes its redacting writer
// (internal/redact's Masker.Writer around standard error) and the same
// masker's Literals. Nothing here raises klog's verbosity, so request bodies
// are never logged regardless.
func LogTo(w io.Writer, secrets SecretSet) {
	klog.LogToStderr(false)
	klog.SetOutput(w)
	logSecrets.Lock()
	logSecrets.set = secrets
	logSecrets.Unlock()
}

// maskSecrets adds values to the set LogTo was given, if any. A static
// value is never forgotten: two sessions may hold the same credential, and
// forgetting it when one ends would unmask it for the other.
func maskSecrets(values ...string) {
	logSecrets.RLock()
	defer logSecrets.RUnlock()
	if logSecrets.set != nil {
		logSecrets.set.Add(values...)
	}
}

// replaceMasked adds a minted token and forgets the one it replaces, which
// was this session's alone and has expired or is about to, so a session
// that lives for hours does not grow the set by one value each refresh.
func replaceMasked(old, current string) {
	logSecrets.RLock()
	defer logSecrets.RUnlock()
	if logSecrets.set == nil {
		return
	}
	logSecrets.set.Add(current)
	if old != "" && old != current {
		logSecrets.set.Forget(old)
	}
}
