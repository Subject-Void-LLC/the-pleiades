// stripURL on an error that names no URL.
package http

import (
	"errors"
	"testing"
)

// TestStripURL_LeavesAnotherErrorAlone: an error that is not a URL's is
// returned as it was.
func TestStripURL_LeavesAnotherErrorAlone(t *testing.T) {
	plain := errors.New("connection refused")
	if got := stripURL(plain); got != plain {
		t.Errorf("stripURL = %v", got)
	}
}
