package hashivault_test

import (
	"bytes"
	"encoding/pem"
	"testing"
)

// pemEncode wraps a DER certificate the way a trust bundle carries it.
//
// Its own file so hashivault_test.go stays about the source rather than
// about certificate plumbing.
func pemEncode(t *testing.T, der []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("encoding the test certificate: %v", err)
	}
	return buf.Bytes()
}
