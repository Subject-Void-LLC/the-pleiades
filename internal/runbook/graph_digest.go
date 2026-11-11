// Package runbook: the digest of a compiled runbook's graph, a frozen wire
// encoding the Controller pins on a job and the Runner holds its own copy to.
//
// It is not a hash of a Go struct's JSON, nor of DAG.Version's encoding, so
// no change to a type, a library or the version's rules can move it. Each
// scheme is a fixed encoding named by its id (GraphSchemeV1): a new scheme is
// added beside the old, never changed, and a reader refuses a scheme it does
// not know rather than guessing (docs/13-releases-and-stability.md).
package runbook

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"sort"
)

// GraphSchemeV1 is the first graph digest scheme, and the one this build
// writes (GraphScheme).
//
// Its encoding, fed to SHA-256: the scheme id itself, then, for each node in
// byte order of its id, the node id, its method, its marker flag, its
// authored flag, and the number of its rollback: steps followed by each
// step's method. Every string is its length as an 8-byte big-endian integer
// followed by its bytes; each flag is one byte, 0 or 1; the step count is an
// 8-byte big-endian integer. The digest is "sha256:" and 64 lowercase hex
// digits.
const GraphSchemeV1 = "graph/1"

// GraphScheme is the scheme this build pins and dispatches.
const GraphScheme = GraphSchemeV1

// GraphDigestPrefix opens every digest a scheme returns.
const GraphDigestPrefix = "sha256:"

// ErrUnknownGraphScheme is returned for a scheme this build does not know.
var ErrUnknownGraphScheme = fmt.Errorf("unknown graph digest scheme")

// GraphDigest is nodes' digest under GraphScheme: what each node of a
// compiled runbook runs, its rollback: lists included, which DAG.Version
// leaves out (engine's forwardDefinition clears them before hashing).
func GraphDigest(nodes map[string]Node) string {
	digest, _ := DigestUnder(GraphScheme, nodes)
	return digest
}

// DigestUnder is nodes' digest under scheme, or ErrUnknownGraphScheme.
func DigestUnder(scheme string, nodes map[string]Node) (string, error) {
	switch scheme {
	case GraphSchemeV1:
		return digestV1(nodes), nil
	default:
		return "", fmt.Errorf("%w %q", ErrUnknownGraphScheme, scheme)
	}
}

// digestV1 is GraphSchemeV1's encoding, as its doc comment states it.
func digestV1(nodes map[string]Node) string {
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	// sort.Strings orders by bytes, not by any Unicode table.
	sort.Strings(ids)
	h := sha256.New()
	writeString(h, GraphSchemeV1)
	for _, id := range ids {
		n := nodes[id]
		writeString(h, id)
		writeString(h, n.Method)
		writeFlag(h, n.Marker)
		writeFlag(h, n.Authored)
		writeLength(h, len(n.Rollback))
		for _, step := range n.Rollback {
			writeString(h, step)
		}
	}
	return GraphDigestPrefix + hex.EncodeToString(h.Sum(nil))
}

// writeLength writes n, a length or a count and so never negative, as an
// 8-byte big-endian integer.
func writeLength(h hash.Hash, n int) {
	if n < 0 {
		n = 0
	}
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(n))
	_, _ = h.Write(b[:])
}

// writeString writes s's length, then its bytes.
func writeString(h hash.Hash, s string) {
	writeLength(h, len(s))
	_, _ = h.Write([]byte(s))
}

// writeFlag writes a flag as one byte, 1 for set.
func writeFlag(h hash.Hash, set bool) {
	if set {
		_, _ = h.Write([]byte{1})
		return
	}
	_, _ = h.Write([]byte{0})
}
