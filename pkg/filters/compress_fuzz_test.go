package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// FuzzGzipDecompress fuzzes GzipDecompress against malformed and
// truncated gzip streams. GzipDecompress is the one function in this
// phase that decodes untrusted, structurally rich binary input the same
// way pki.go's PEMToDER/DERToPEM do, matching this package's own "every
// non-trivial parser gets a fuzz target" convention (pki_fuzz_test.go's
// own comment) even though the checklist names only SyslogParse and
// PathJoin/PathExtractExtension explicitly. GzipCompress is not fuzzed
// separately: it only ever encodes well-formed input of its own
// creation, with no adversarial decode path of its own.
func FuzzGzipDecompress(f *testing.F) {
	valid := filters.GzipCompress("hello, world")
	f.Add(valid)
	f.Add(valid[:len(valid)/2]) // truncated mid-stream
	f.Add([]byte{})
	f.Add([]byte("plain text, not gzip at all"))
	f.Add([]byte{0x1f, 0x8b}) // real gzip magic bytes, nothing after
	f.Fuzz(func(t *testing.T, data []byte) {
		filters.GzipDecompress(data)
	})
}
