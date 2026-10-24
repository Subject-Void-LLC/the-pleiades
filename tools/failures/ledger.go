// Reading and writing a ledger: one JSON entry per line, appended to without
// duplicates and rewritten whole, so a reader never finds half a file.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// defaultDir is where the ledgers live: gitignored, since make pr-check
// appending to a tracked file would dirty the tree and refuse its own
// receipt, and since the repository is public.
const defaultDir = ".IGNORE/ci"

// ledgerFile is the file the ledger of source lives in, under dir.
func ledgerFile(dir, source string) string {
	return filepath.Join(dir, source+".jsonl")
}

// readLedger reads every entry in the ledger at path, refusing one this
// reader cannot read (another major version, or not JSON) with its line
// number. A ledger that does not exist yet is empty.
func readLedger(path string) ([]Entry, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- a ledger under the directory this tool was pointed at
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; scanner.Scan(); n++ {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		if err := e.check(); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		out = append(out, e)
	}
	return out, scanner.Err()
}

// appendLedger adds each entry the ledger at path does not already hold, and
// returns how many it added. An entry is the same as another when its key
// is: a run recorded again adds nothing.
func appendLedger(path string, entries []Entry) (int, error) {
	have, err := readLedger(path)
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	for _, e := range have {
		seen[e.key()] = true
	}
	var fresh []Entry
	for _, e := range entries {
		if k := e.key(); !seen[k] {
			seen[k] = true
			fresh = append(fresh, e)
		}
	}
	if len(fresh) == 0 {
		return 0, nil
	}
	return len(fresh), writeLedger(path, append(have, fresh...))
}

// writeLedger writes entries to path through a temporary file renamed over
// it, creating its directory private to this user.
func writeLedger(path string, entries []Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var b bytes.Buffer
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// readAll reads both ledgers under dir, pr-check's first.
func readAll(dir string) ([]Entry, error) {
	var all []Entry
	for _, source := range KnownSources {
		entries, err := readLedger(ledgerFile(dir, source))
		if err != nil {
			return nil, err
		}
		all = append(all, entries...)
	}
	return all, nil
}
