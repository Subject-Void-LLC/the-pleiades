// The collection coverage pages: what each collection's ledger
// (coverage/collections) adds up to, against its endpoint's own contract.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/coverageledger"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
)

// coverageSchemaVersion is the version of collection-coverage.json's shape,
// raised when a field changes meaning or goes away.
const coverageSchemaVersion = 1

// coverageOSes are the proof columns, in the order every page shows them.
var coverageOSes = []coverageledger.OS{coverageledger.OSLinux, coverageledger.OSFreeBSD, coverageledger.OSDarwin, coverageledger.OSWindows}

// generateCoverage writes outDir/coverage/index.md, one page per collection
// with a ledger, and outDir/schemas/collection-coverage.json, removing any
// page of a collection whose ledger is gone.
func generateCoverage(outDir string) error {
	ledgers, err := coverageledger.LoadAll(".")
	if err != nil {
		return err
	}
	floors, err := coverageledger.LoadFloors(".")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(catalogdata.Collections))
	for _, cfg := range catalogdata.Collections {
		names = append(names, cfg.Name)
	}

	dir := filepath.Join(outDir, "coverage")
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- generated docs, not secret material
		return err
	}
	written := map[string]bool{}
	var reports []coverageledger.Report
	measured := map[string]bool{}
	for _, l := range ledgers {
		// A ledger breaking its own rules is refused with them, not rendered
		// (a proof's malformed test reference would otherwise be guessed at).
		if problems := l.Problems(); len(problems) > 0 {
			return fmt.Errorf("coverage ledger %s:\n  %s", l.Contract.Collection, strings.Join(problems, "\n  "))
		}
		methods := coverageledger.MethodsIn(l.Contract.Collection, names)
		for _, m := range methods {
			measured[m] = true
		}
		r := l.Compute(methods)
		reports = append(reports, r)
		path := filepath.Join(dir, r.Collection+".md")
		if err := os.WriteFile(path, []byte(coveragePage(r)), 0o644); err != nil { // #nosec G306 -- generated docs, not secret material
			return err
		}
		written[path] = true
	}
	index := filepath.Join(dir, "index.md")
	if err := os.WriteFile(index, []byte(coverageIndex(reports, floors, names, measured)), 0o644); err != nil { // #nosec G306 -- generated docs, not secret material
		return err
	}
	written[index] = true
	if err := removeStalePages(dir, written); err != nil {
		return err
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	doc := struct {
		SchemaVersion int                     `json:"schemaVersion"`
		Collections   []coverageledger.Report `json:"collections"`
	}{coverageSchemaVersion, reports}
	if err := enc.Encode(doc); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "schemas", "collection-coverage.json"), buf.Bytes(), 0o644) // #nosec G306 -- generated docs, not secret material
}

// coverageIndex renders the landing page: one row per measured collection,
// then each namespace's methods no ledger measures yet.
func coverageIndex(reports []coverageledger.Report, floors coverageledger.FloorFile, names []string, measured map[string]bool) string {
	var b strings.Builder
	b.WriteString(frontMatter("beta"))
	b.WriteString("# Collection coverage\n\n")
	b.WriteString("How much of each endpoint's own contract (its OpenAPI document or CLI reference) a collection " +
		"implements, and how much of that is proven against the real thing. **Mapped** is the share of the contract's " +
		"operations in scope that some method implements; **live** is the share whose every method has a test against " +
		"a real server, device or tool. An operation is out of scope only with a written reason, shown on the " +
		"collection's page. A proof is declared, and checked only to name a test that exists and reaches the method: " +
		"it does not say the test ran, or that it covers every option of the operation.\n\n" +
		"Each collection's numbers may not drop below its floor (`" + coverageledger.FloorFileName + "`).\n\n")
	var rows [][]string
	for _, r := range reports {
		floor := "none yet"
		if f, ok := floors.Floors[r.Collection]; ok {
			floor = fmt.Sprintf("%.1f%% / %.1f%%", f.Mapped, f.Live)
		}
		rows = append(rows, []string{
			fmt.Sprintf("[%s](%s.md)", r.Collection, r.Collection), r.Title, code(r.Source.Version),
			itoa(r.Total - r.Excluded), fmt.Sprintf("%.1f%%", r.MappedPercent), fmt.Sprintf("%.1f%%", r.LivePercent), floor,
		})
	}
	b.WriteString(table([]string{"Collection", "Endpoint", "Version", "Operations in scope", "Mapped", "Live", "Floor (mapped / live)"}, rows))

	unmeasured := map[string]int{}
	for _, name := range names {
		if !measured[name] {
			unmeasured[strings.SplitN(name, ".", 2)[0]]++
		}
	}
	b.WriteString("\n## Not measured yet\n\n")
	b.WriteString("These methods belong to no collection with a ledger, so their coverage of their endpoint is not " +
		"measured. Not measured is not zero: it is unknown.\n\n")
	var nsRows [][]string
	for _, ns := range sortedKeys(unmeasured) {
		nsRows = append(nsRows, []string{code(ns), itoa(unmeasured[ns])})
	}
	b.WriteString(table([]string{"Namespace", "Methods"}, nsRows))
	return b.String()
}

// coveragePage renders one collection's page.
func coveragePage(r coverageledger.Report) string {
	var b strings.Builder
	b.WriteString(frontMatter("beta"))
	fmt.Fprintf(&b, "# %s: %s\n\n", r.Collection, r.Title)
	kind := map[coverageledger.SourceKind]string{coverageledger.SourceOpenAPI: "OpenAPI document", coverageledger.SourceCLI: "CLI reference"}[r.Source.Kind]
	fmt.Fprintf(&b, "Contract (%s): <%s>, version %s", kind, r.Source.URL, code(r.Source.Version))
	if r.Source.Revision != "" && r.Source.Revision != r.Source.Version {
		fmt.Fprintf(&b, " (revision %s)", code(r.Source.Revision))
	}
	fmt.Fprintf(&b, ", sha256 %s.\n\n", code(r.Source.SHA256))
	fmt.Fprintf(&b, "**%d operations: %d mapped, %d excluded, %d not implemented. Mapped %.1f%%, live %.1f%% of the %d in scope.**\n\n",
		r.Total, r.Mapped, r.Excluded, r.Unmapped, r.MappedPercent, r.LivePercent, r.Total-r.Excluded)

	b.WriteString("## Operations\n\n")
	var rows [][]string
	for _, op := range r.Ops {
		what := "-"
		switch op.Status {
		case coverageledger.OpMapped:
			links := make([]string, len(op.Methods))
			for i, m := range op.Methods {
				links[i] = moduleLink(m)
			}
			what = strings.Join(links, ", ")
		case coverageledger.OpExcluded:
			what = op.Reason
		}
		live := "-"
		if op.Status == coverageledger.OpMapped {
			live = yesNo(op.Live)
		}
		rows = append(rows, []string{code(op.ID), op.Summary, string(op.Status), what, live})
	}
	b.WriteString(table([]string{"Operation", "Contract summary", "Status", "Method, or why excluded", "Live"}, rows))

	b.WriteString("\n## Methods and their proof\n\n")
	b.WriteString("Per OS: `live` is a test against the real thing, `fixture` one against a stand-in, `-` none.\n\n")
	header := []string{"Method", "Operations"}
	for _, os := range coverageOSes {
		header = append(header, string(os))
	}
	rows = nil
	for _, m := range r.Methods {
		ops := m.WithoutOp
		if len(m.Ops) > 0 {
			quoted := make([]string, len(m.Ops))
			for i, id := range m.Ops {
				quoted[i] = code(id)
			}
			ops = strings.Join(quoted, ", ")
		}
		row := []string{moduleLink(m.FQCN), ops}
		for _, os := range coverageOSes {
			row = append(row, proofCell(m.Proof[os]))
		}
		rows = append(rows, row)
	}
	b.WriteString(table(header, rows))
	return b.String()
}

// proofCell renders one method's proof on one OS.
func proofCell(p coverageledger.Proof) string {
	if p.Status == "" || p.Status == coverageledger.ProofNone {
		return "-"
	}
	_, name, ok := coverageledger.SplitTest(p.Test)
	if !ok {
		return string(p.Status)
	}
	return fmt.Sprintf("%s (`%s`)", p.Status, name)
}

// moduleLink links fqcn to its reference page from a coverage page.
func moduleLink(fqcn string) string {
	return fmt.Sprintf("[%s](../modules/%s.md)", fqcn, strings.ReplaceAll(fqcn, ".", "/"))
}
