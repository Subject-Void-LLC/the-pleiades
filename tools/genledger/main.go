// Command genledger refreshes each OpenAPI-sourced collection's contract.json
// from the contract file vendored beside it: the operation list through
// coverageledger.ExtractOpenAPI and the file's sha256. A CLI-sourced contract
// is listed by hand and left alone. Run it from the repository root after
// replacing a vendored contract with a newer release's:
//
//	go run ./tools/genledger
//
// internal/archtest's ledger test fails while a contract.json disagrees with
// its vendored file, so a contract that moved without this run cannot merge.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/internal/coverageledger"
)

func main() {
	if err := run("."); err != nil {
		fmt.Fprintln(os.Stderr, "genledger:", err)
		os.Exit(1)
	}
}

// run refreshes every OpenAPI contract under root and says how many it wrote.
func run(root string) error {
	ledgers, err := coverageledger.LoadAll(root)
	if err != nil {
		return err
	}
	written := 0
	for _, l := range ledgers {
		if l.Contract.Source.Kind != coverageledger.SourceOpenAPI {
			continue
		}
		changed, err := refresh(l)
		if err != nil {
			return fmt.Errorf("%s: %w", l.Contract.Collection, err)
		}
		if changed {
			written++
		}
	}
	fmt.Printf("genledger: %d contract(s) rewritten of %d ledger(s)\n", written, len(ledgers))
	return nil
}

// refresh rewrites l's contract.json from its vendored file, reporting whether
// anything changed.
func refresh(l coverageledger.Ledger) (bool, error) {
	doc, err := l.ReadSource()
	if err != nil {
		return false, err
	}
	ops, err := coverageledger.ExtractOpenAPI(doc)
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(doc)
	c := l.Contract
	c.Source.SHA256 = hex.EncodeToString(sum[:])
	c.Ops = ops

	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	// Summaries carry & and < as written; escaping them would only make the
	// file harder to read.
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		return false, err
	}
	// Read and written through a root on the ledger's directory, as
	// ReadSource reads, so a contract.json that is a symbolic link cannot
	// send the write outside it.
	root, err := os.OpenRoot(l.Dir)
	if err != nil {
		return false, err
	}
	defer func() { _ = root.Close() }()
	old, err := root.ReadFile("contract.json")
	if err != nil {
		return false, err
	}
	if bytes.Equal(old, out.Bytes()) {
		return false, nil
	}
	return true, root.WriteFile("contract.json", out.Bytes(), 0o644) // #nosec G306 -- a committed data file, not secret material
}
