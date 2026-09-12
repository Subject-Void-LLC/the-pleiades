// This file states the run journal's guarantee as a property instead of
// as a list of examples: whatever a task's fqcn, a method's stat keys and
// values, an emitted inverse's own fqcn, a failure's error text or a
// skip's reason sentence happen to be, none of it reaches a journal
// entry, and every string that does reach one is drawn from a set this
// file can enumerate without looking at the input at all.
//
// The design note's Section 9 asks for exactly this target, over
// arbitrary Stats maps (arbitrary top-level keys, nested maps, byte
// slices), an arbitrary Err string, an arbitrary SkipReason, an
// arbitrary Task.FQCN and an arbitrary inverse fqcn, asserting three
// things. All three are here and they are deliberately different in
// kind, because each covers the others' blind spot:
//
//  1. The whitelist. Every field of every produced entry is drawn from an
//     enumerable set. This is the strongest of the three and the one that
//     cannot be fooled: if a fuzzed value reaches an entry, it can only
//     do so by already being a name this file names.
//  2. No input substring survives. Weaker, and kept anyway, because the
//     whitelist can only check fields somebody thought to check. This one
//     marshals the whole entry, so it covers a field a later phase adds
//     and nobody remembers to add an assertion for.
//  3. An unregistered FQCN never appears in output, which is the specific
//     failure the FQCNUnregistered sentinel exists to prevent.
//
// internal/engine blank-imports no Collection package, so the registry is
// empty here and every fqcn resolves to an engine action or to the
// sentinel. That is a narrower registry than a real run has and it is the
// right one for this property: what is under test is the projection's own
// admission rule, not the catalog's contents. The catalog half is
// internal/archtest's TestEveryProjectedKeyNameIsRegistryDeclared, which
// drives the same projection over every registered FQCN with the whole
// catalog loaded.
package engine

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// journalFuzzInput is one fuzz iteration's inputs, gathered into a type
// so the property helpers below take one argument instead of seven and
// so the baseline (below) is built by the same code path as a real
// iteration rather than by a second copy of it.
type journalFuzzInput struct {
	FQCN        string
	StatKey     string
	StatValue   string
	InverseFQCN string
	ErrText     string
	SkipReason  string
	Blob        []byte
}

// values returns every input string, for the substring property. The
// blob is included as a string because that is how a leak of it would
// appear: NodeResult.Stats is map[string]interface{}, so a []byte value
// marshals into an entry as text if anything ever copied one there.
func (in journalFuzzInput) values() []string {
	return []string{in.FQCN, in.StatKey, in.StatValue, in.InverseFQCN, in.ErrText, in.SkipReason, string(in.Blob)}
}

// journalFuzzProject runs the projection over one input.
//
// It builds two results, not one, because a failure and a skip carry
// different fuzzed text and neither shape can carry the other's:
// projectResult settles a non-nil Err before it ever looks at Skipped,
// so a single result could never exercise SkipReason at all.
//
// The stats are built to be adversarial in three separate ways at once.
// The fuzzed key carries the fuzzed value at top level. The fuzzed blob
// is planted under "stdout", a key the engine's own transport table
// really does declare, so the key is admitted while its value must still
// never appear. And both pkg/sdk records carry the same value one and
// two levels down, which is where the never-recurse rule has to hold.
func journalFuzzProject(in journalFuzzInput) ([]JournalEntry, error) {
	task := &Task{
		Name:     "fixed-task-name",
		Register: "fixed-register",
		FQCN:     in.FQCN,
		Params: map[string]interface{}{
			in.StatKey: in.StatValue,
			"command":  string(in.Blob),
		},
	}
	r := &run{
		dag: &DAG{
			ID:      "fixed-dag-id",
			Version: "sha256:fixed",
			Nodes:   map[string]*Task{"tasks[0]": task},
		},
		runID: "fixed-run-id",
	}

	failed := NodeResult{
		NodeID: "tasks[0]",
		Device: "fixed-device",
		Stats: map[string]interface{}{
			in.StatKey: in.StatValue,
			"stdout":   in.Blob,
			sdk.StatDiff: map[string]interface{}{
				"before": map[string]interface{}{"content": in.StatValue, in.StatKey: in.Blob},
				"after":  map[string]interface{}{"content": in.StatValue},
			},
			sdk.StatInverse: map[string]interface{}{
				inverseRecordFQCN: in.InverseFQCN,
				inverseRecordParams: map[string]interface{}{
					in.StatKey: in.StatValue,
					"content":  string(in.Blob),
				},
			},
		},
	}
	failed.fail(FailureStageAction, testError(in.ErrText))

	// A skip carries no stats, which is what a skipped node really
	// produces: runNode returns before any action runs. Its whole fuzzed
	// surface is the reason sentence, which quotes the author's own
	// expression and must never be journaled.
	skipped := NodeResult{
		NodeID:      "tasks[0]",
		Skipped:     true,
		SkipReason:  in.SkipReason,
		skipKind:    SkipKindWhen,
		skipOrdinal: 1,
		skipTotal:   2,
	}

	return r.projectLevel([][]NodeResult{{failed}, {skipped}})
}

// journalFuzzBaseline is what the projection produces for entirely empty
// input, marshaled.
//
// It is the substring property's own control, and without it that
// property is unusable rather than merely weak. An entry serializes to
// field names, punctuation, fixed identifiers, closed-enum values and a
// zero timestamp, and a short fuzzed input collides with those by
// accident constantly: an input of "a" is a substring of "TaskName", an
// input of "0001-01" is a substring of the zero time. None of those is a
// leak. Anything already present when no input was supplied cannot have
// come from the input, so it is explained once, here, rather than
// excepted case by case forever.
//
// It is computed once for the whole fuzz run. The projection is
// deterministic and holds no state across calls, so a per-iteration
// rebuild would burn a marshal on every execution to produce the same
// bytes.
var journalFuzzBaseline = sync.OnceValue(func() string {
	entries, err := journalFuzzProject(journalFuzzInput{})
	if err != nil {
		panic("journal fuzz baseline: the projection refused an empty input: " + err.Error())
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		panic("journal fuzz baseline: " + err.Error())
	}
	return string(encoded)
})

// journalFuzzAlphabet is every string the projection is allowed to put
// into an entry that the baseline does not already contain: the engine's
// own action names, the stat keys those actions declare, the two pkg/sdk
// stat constants, and the sentinel.
//
// This is the same set the whitelist property checks field by field,
// restated as a flat list so the substring property can explain a
// legitimate survival. A fuzzed value that happens to be exactly
// "ssh_exec" really does appear in the output, as a resolved FQCN, and
// that is the projection working rather than leaking.
// Every element is stored JSON escaped, because that is the form a
// needle arrives in and the form the marshaled entry holds. Only the
// sentinel is actually affected today, since encoding/json escapes its
// angle brackets, but comparing a raw alphabet against an escaped needle
// is the kind of near-miss that reads as correct right up until it is
// not.
var journalFuzzAlphabet = sync.OnceValue(func() []string {
	alphabet := []string{FQCNUnregistered, sdk.StatInverse, sdk.StatDiff}
	for name, statKeys := range engineActionStatKeys {
		alphabet = append(alphabet, name)
		alphabet = append(alphabet, statKeys...)
	}
	for i, name := range alphabet {
		alphabet[i] = journalJSONEscape(name)
	}
	return alphabet
})

// journalFuzzExplains reports whether needle appearing in a marshaled
// entry has an explanation other than a leak.
//
// Two explanations count. The needle is part of what an empty input
// already produces, so nothing about the input put it there. Or the
// needle is part of a name the projection is independently allowed to
// store, which is the case where a fuzzed value happens to spell a
// registered method or a declared key.
//
// The residual gap is worth stating rather than leaving for a reader to
// find. A needle that straddles a boundary between two legitimate pieces
// of output, "noop\",\"FQCNUnresolved" for instance, is explained by
// neither and would be reported. Nothing generates such a string by
// mutation in practice, and the whitelist property is what actually
// carries the guarantee; this one is defense in depth over fields the
// whitelist does not know about.
func journalFuzzExplains(needle string) bool {
	if needle == "" {
		return true
	}
	if strings.Contains(journalFuzzBaseline(), needle) {
		return true
	}
	for _, allowed := range journalFuzzAlphabet() {
		if strings.Contains(allowed, needle) {
			return true
		}
	}
	return false
}

// FuzzProjectLevel is the property test this file's own doc comment
// describes.
//
// The seed corpus is the four nastiest real shapes rather than four
// arbitrary strings, because a seed is what the mutation engine starts
// from and a corpus of toy values explores a neighborhood of toy values:
//
//   - a full running-config-shaped block, which is what net.ios.config's
//     backup stat really holds and the single worst value in the catalog
//     (its own Doc.Returns entry says it contains the device's enable
//     secret and every shared key it has);
//   - a PEM private key body, which is what a file-generating credential
//     injector puts on the wire;
//   - a value that contains the task's own resolved FQCN as a substring,
//     which is the case a naive "does the input appear in the output"
//     rule gets wrong in both directions;
//   - a stat key whose name collides with a key the engine's own
//     transport table declares, so the key is legitimately admitted while
//     the value under it must still never appear.
func FuzzProjectLevel(f *testing.F) {
	f.Add("pkg.apt.install", "stdout", "s3cr3t", "file.copy", "connection refused", "when: false", []byte("payload"))
	f.Add("", "", "", "", "", "", []byte(nil))
	f.Add("noop", "diff", "the whole prior file", "", "task tasks[0] failed", "", []byte("0644"))

	// The running-config, verbatim in shape: the enable secret, a local
	// user hash and a TACACS key, exactly the three things net.ios.config
	// warns its backup stat carries.
	f.Add("net.ios.config", "backup", "Building configuration...\n!\nhostname edge1\n!\nenable secret 5 $1$mERr$Xk8vT1kQ7\n"+
		"username admin privilege 15 secret 5 $1$abcd$Zz9\n!\ntacacs-server key 7 060506324F41\n!\nend\n",
		"net.ios.config", "", "", []byte("show running-config"))

	// A PEM block, the shape a file injector and an envelope both produce.
	f.Add("ssh_exec", "stdout", "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0Z8vK\n-----END RSA PRIVATE KEY-----\n",
		"ssh_exec", "permission denied", "when_cel: false", []byte("-----BEGIN CERTIFICATE-----\nMIIC\n-----END CERTIFICATE-----"))

	// A value carrying the resolved FQCN as a substring, and a stat key
	// colliding with a declared one.
	f.Add("ssh_exec", "stdout", "ssh_exec ran and the password is hunter2", "serial_exec", "ssh_exec failed", "stdout", []byte("stdout"))

	f.Fuzz(func(t *testing.T, fqcn, statKey, statValue, inverseFQCN, errText, skipReason string, blob []byte) {
		in := journalFuzzInput{
			FQCN:        fqcn,
			StatKey:     statKey,
			StatValue:   statValue,
			InverseFQCN: inverseFQCN,
			ErrText:     errText,
			SkipReason:  skipReason,
			Blob:        blob,
		}

		entries, err := journalFuzzProject(in)
		if err != nil {
			t.Fatalf("projectLevel refused a well formed level: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("projectLevel produced %d entries for one failure and one skip", len(entries))
		}

		assertJournalWhitelist(t, entries[0], entries[1])
		assertNoInputSurvived(t, in, entries)
	})
}

// assertJournalWhitelist is property 1 and property 3: every field of
// both entries is drawn from an enumerable set, and an unregistered FQCN
// is recorded as the sentinel rather than as itself.
func assertJournalWhitelist(t *testing.T, failed, skipped JournalEntry) {
	t.Helper()

	for _, e := range []JournalEntry{failed, skipped} {
		// Every fixed field is exactly what journalFuzzProject set.
		// Anything else means a value crossed into a field it has no
		// business in.
		fixed := map[string]string{
			"RunID": e.RunID, "NodeID": e.NodeID, "DAGID": e.DAGID, "DAGVersion": e.DAGVersion,
			"TaskName": e.TaskName, "Register": e.Register,
		}
		want := map[string]string{
			"RunID": "fixed-run-id", "NodeID": "tasks[0]", "DAGID": "fixed-dag-id",
			"DAGVersion": "sha256:fixed", "TaskName": "fixed-task-name", "Register": "fixed-register",
		}
		for field, got := range fixed {
			if got != want[field] {
				t.Errorf("%s = %q, want the fixed %q", field, got, want[field])
			}
		}

		assertResolvedName(t, "FQCN", e.FQCN, e.FQCNUnresolved)
		assertResolvedName(t, "InverseFQCN", e.InverseFQCN, e.InverseFQCNUnresolved)

		// A stat key survives only if the engine's own action table
		// declares it for the resolved action, or it is one of the two
		// pkg/sdk stat constants. Nothing else can be named, whatever the
		// input was.
		allowed := nameSet(engineActionStatKeys[e.FQCN])
		for _, key := range e.StatKeys {
			if key == sdk.StatInverse || key == sdk.StatDiff {
				continue
			}
			if !allowed[key] {
				t.Errorf("StatKeys named %q, which neither pkg/sdk nor %q's own declared keys admit", key, e.FQCN)
			}
		}

		// No engine action carries a Doc, so no param key of either kind
		// may ever be named here. A non-empty vector means the admission
		// rule let something through on a set it should have found empty.
		if len(e.ParamKeys) != 0 {
			t.Errorf("ParamKeys = %v, want none: no engine action declares Doc.Params", e.ParamKeys)
		}
		if len(e.InverseParamKeys) != 0 {
			t.Errorf("InverseParamKeys = %v, want none for the same reason", e.InverseParamKeys)
		}

		// Both belong to a Walk-tier dispatch that internal/engine knows
		// nothing about, so the projection must leave both alone whatever
		// it was handed.
		if e.JobID != "" || e.Attempt != 0 {
			t.Errorf("recorded JobID %q attempt %d, want both left for the Walk sink to stamp", e.JobID, e.Attempt)
		}
	}

	// DeviceID is checked per entry rather than in the loop because the
	// two results carry different ones on purpose: a failed device
	// execution names its device, and a condition skip has none to name.
	if failed.DeviceID != "fixed-device" {
		t.Errorf("the failure recorded DeviceID %q, want the fixed one", failed.DeviceID)
	}
	if skipped.DeviceID != "" {
		t.Errorf("the condition skip recorded DeviceID %q, want none: it resolved no device", skipped.DeviceID)
	}
	if failed.Outcome != OutcomeFailed || failed.FailureStage != FailureStageAction {
		t.Errorf("recorded outcome %q stage %q, want the tagged action failure", failed.Outcome, failed.FailureStage)
	}
	if failed.SkipKind != SkipKindNone {
		t.Errorf("recorded skip kind %q on a failure", failed.SkipKind)
	}
	if skipped.Outcome != OutcomeSkipped || skipped.SkipKind != SkipKindWhen {
		t.Errorf("recorded outcome %q kind %q, want the tagged when skip", skipped.Outcome, skipped.SkipKind)
	}
	if skipped.FailureStage != FailureStageNone {
		t.Errorf("recorded failure stage %q on a skip", skipped.FailureStage)
	}
	if skipped.SkipOrdinal != 1 || skipped.SkipTotal != 2 {
		t.Errorf("recorded ordinal %d of %d, want the numbers the condition reported", skipped.SkipOrdinal, skipped.SkipTotal)
	}
}

// assertNoInputSurvived is property 2: marshal the entries and fail if
// any input value appears in the result without an explanation.
//
// Both the entries and each needle go through encoding/json, so the two
// are escaped identically. Searching for a raw input inside escaped
// output would silently miss any value carrying a quote, a backslash or
// a control byte, which is most of what a fuzzer produces and all of
// what a running-config contains.
func assertNoInputSurvived(t *testing.T, in journalFuzzInput, entries []JournalEntry) {
	t.Helper()

	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshaling the projected entries: %v", err)
	}
	rendered := string(encoded)

	for _, value := range in.values() {
		needle := journalJSONEscape(value)
		if !strings.Contains(rendered, needle) {
			continue
		}
		if journalFuzzExplains(needle) {
			continue
		}
		t.Errorf("the input %q survived into the journal: %s\n"+
			"A journal entry stores key NAMES, counts, closed enums and platform identifiers. "+
			"Nothing a device, a credential store, a decrypted envelope or an injector produced "+
			"may reach one.", value, rendered)
	}
}

// journalJSONEscape returns how encoding/json would render value inside
// a JSON document, with the surrounding quotes stripped, so a search for
// it inside a marshaled entry compares like with like.
//
// It panics rather than returning an error because encoding/json cannot
// fail on a string: an invalid UTF-8 byte is replaced with U+FFFD, which
// is exactly what happens to the same bytes inside the marshaled entry,
// so the two stay comparable.
//
// The quotes come off one at a time rather than through strings.Trim,
// which strips every leading and trailing quote it finds and would eat
// the escaped one out of a value that itself ends in a quote character.
// A fuzzer produces those constantly.
func journalJSONEscape(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic("journal fuzz: marshaling a string failed: " + err.Error())
	}
	return strings.TrimSuffix(strings.TrimPrefix(string(encoded), `"`), `"`)
}

// assertResolvedName fails unless name is a name resolution is allowed to
// produce: a registered descriptor's own name, an engine action's own
// table key, the FQCNUnregistered sentinel with its flag set, or the
// empty string for a node that named no method.
func assertResolvedName(t *testing.T, field, name string, unresolved bool) {
	t.Helper()
	if name == FQCNUnregistered {
		if !unresolved {
			t.Errorf("%s holds the sentinel with the unresolved flag clear", field)
		}
		return
	}
	if unresolved {
		t.Errorf("%s = %q with the unresolved flag set; a dropped name must be the sentinel", field, name)
	}
	if name == "" {
		return
	}
	if _, ok := engineActionStatKeys[name]; ok {
		return
	}
	if _, ok := collection.Lookup(name); ok {
		return
	}
	t.Errorf("%s = %q, which is neither a registered method, an engine action, nor the sentinel", field, name)
}

// TestJournalFuzzHelpersDetectALeak is the negative control for the two
// properties above, and it exists for the same reason every other rule in
// this phase carries one: a property test that could not fail is
// indistinguishable from one that passes.
//
// It proves three things about the helpers rather than about the
// projection: an explanation really is required (an arbitrary value is
// not explained), a legitimate survival really is explained (a resolved
// engine action name is), and the baseline really does absorb the
// structural bytes an entry always carries (a field name is explained).
func TestJournalFuzzHelpersDetectALeak(t *testing.T) {
	cases := []struct {
		needle string
		want   bool
		why    string
	}{
		{"the whole prior file", false, "an arbitrary value has no explanation and must be reported"},
		{"ssh_exec", true, "a resolved engine action name is a name the projection may store"},
		{"stdout", true, "a stat key the engine's transport table declares may be named"},
		{"TaskName", true, "a field name is in every entry, input or not, so the baseline explains it"},
		{"0001-01-01", true, "the zero timestamp is structural, and a short input colliding with it is not a leak"},
		{"", true, "the empty string is a substring of everything and can prove nothing"},
	}
	for _, tc := range cases {
		if got := journalFuzzExplains(tc.needle); got != tc.want {
			t.Errorf("journalFuzzExplains(%q) = %v, want %v: %s", tc.needle, got, tc.want, tc.why)
		}
	}

	// And the whole assertion fires end to end when a value really does
	// reach an entry. The leak is planted in a field the whitelist does
	// not inspect at all, which is exactly the case property 2 exists to
	// cover.
	leaked := JournalEntry{
		RunID: "fixed-run-id", NodeID: "tasks[0]", DAGID: "fixed-dag-id", DAGVersion: "sha256:fixed",
		TaskName: "fixed-task-name", Register: "PROBE-LEAKED-DEVICE-OUTPUT", Outcome: OutcomeRan,
	}
	probe := &testing.T{}
	assertNoInputSurvived(probe, journalFuzzInput{StatValue: "PROBE-LEAKED-DEVICE-OUTPUT"}, []JournalEntry{leaked})
	if !probe.Failed() {
		t.Error("assertNoInputSurvived accepted an entry carrying an input value verbatim, so it reports nothing")
	}
}

// TestFuzzProjectLevelSeedCorpus runs the seed corpus under `go test`
// with no -fuzz flag, which is what every ordinary run and every CI job
// does.
//
// It is not redundant with the fuzz target: `go test` already replays
// seeds, but only as part of FuzzProjectLevel itself. This exists so the
// nastiest cases are also named in a failure message a reader can act on,
// and so a coverage run attributes them.
func TestFuzzProjectLevelSeedCorpus(t *testing.T) {
	cases := []struct {
		name string
		in   journalFuzzInput
	}{
		{"running-config", journalFuzzInput{
			FQCN: "net.ios.config", StatKey: "backup", InverseFQCN: "net.ios.config",
			StatValue: "enable secret 5 $1$mERr$Xk8vT1kQ7\ntacacs-server key 7 060506324F41\n",
		}},
		{"pem", journalFuzzInput{
			FQCN: "ssh_exec", StatKey: "stdout", InverseFQCN: "ssh_exec",
			StatValue: "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----\n",
		}},
		{"value-contains-fqcn", journalFuzzInput{
			FQCN: "ssh_exec", StatKey: "stdout", InverseFQCN: "serial_exec",
			StatValue: "ssh_exec ran and the password is hunter2",
		}},
		{"colliding-key", journalFuzzInput{
			FQCN: "ssh_exec", StatKey: "stdout", StatValue: "hunter2", InverseFQCN: "ssh_exec",
			Blob: []byte("root:$6$saltsalt$hash"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := journalFuzzProject(tc.in)
			if err != nil {
				t.Fatalf("projectLevel refused %s: %v", tc.name, err)
			}
			assertJournalWhitelist(t, entries[0], entries[1])
			assertNoInputSurvived(t, tc.in, entries)
		})
	}
}
