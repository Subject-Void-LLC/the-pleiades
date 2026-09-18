// Tests that hold every message to the wording rules and pin the derived
// numbers they quote.
package setup

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// everyMessage renders every message this command can print, with realistic
// arguments, so one test can hold all of them to the same rules.
func everyMessage() map[string]string {
	fp := crypto.Fingerprint([]byte(strings.Repeat("m", 32)))
	key := []byte(strings.Repeat("m", 32))
	census := &crypto.Census{Columns: []crypto.ColumnCensus{
		{Noun: "credentials", Sealed: 47, Opens: map[string]int{"the key in /srv/.env": 47}},
		{Noun: "devices", Sealed: 213, Opens: map[string]int{"the key in /srv/.env": 200, PublishedComposeKeyName: 13}},
		{Noun: "saved launch configurations", Sealed: 1, Opens: map[string]int{"the key in /srv/.env": 1}},
		{Noun: "mesh signing keys"},
	}}
	firstWrite := JudgeKey(KeyContext{File: "/srv/.env", Database: `"/data/controller.db"`, Census: census})
	replaceProtected := JudgeKey(KeyContext{File: "/srv/.env", Database: "postgres://pleiades@postgres:5432/pleiades", Census: census, HeldKey: fp, Held: []string{"the key in /srv/.env"}, Replacing: true})
	replaceUncounted := JudgeKey(KeyContext{File: "/srv/.env", HeldKey: fp, Replacing: true})

	msgs := map[string]string{
		"recovery matrix":         recoveryMatrix(),
		"outage question":         OutageQuestion(),
		"lowering notice":         LoweringNotice(topology.OutageBudget(2*time.Hour), topology.OutageBudget(10*time.Minute)),
		"confirmation prompt":     confirmationPrompt("/srv/.env", "postgres://pleiades@postgres:5432/pleiades", fp, 3),
		"possession screen":       possessionScreen(crypto.EncodeKey(key), fp, "/srv/.env"),
		"ctrl-c notice":           ctrlCNotice,
		"possession passed":       possessionMatched,
		"possession mismatch":     possessionMismatch(1),
		"possession failed":       possessionFailed().Message,
		"not checked":             notCheckedNotice("/srv/.env"),
		"nothing counted":         nothingCountedNotice(),
		"already set up":          alreadySetUp("/srv/.env", fp).Message,
		"needs force":             needsForce("/srv/.env", "a JWT_SECRET").Message,
		"first write refusal":     firstWrite.Error(),
		"replace refusal":         replaceProtected.Error(),
		"replace without a count": replaceUncounted.Error(),
		"compose note":            strings.Join(composeFileNote(), "\n"),
		"compose summary":         composeSummary("/srv/.env", &Plan{Key: Generate, JWT: Replace, Budget: Generate, BudgetValue: topology.DefaultOutageBudget}, key),
		"helm summary":            helmSummary("/out/pleiades-secret.yaml", "/out/pleiades-values.yaml", Options{Namespace: "pleiades"}, key),
		"helm secret header":      secretHeader("pleiades-secrets"),
		"helm values header":      valuesHeader("pleiades-secrets", topology.OutageBudget(2*time.Hour)),
	}
	for _, b := range []time.Duration{time.Minute, 30 * time.Minute, 2 * time.Hour, 12 * time.Hour} {
		msgs["outage explanation "+b.String()] = OutageExplanation(topology.OutageBudget(b))
	}
	return msgs
}

// forbidden are the words and characters no message may use. "may", "might",
// "possibly" and "could potentially" hedge what is certain; "data loss" is
// the abstraction that gets clicked through, where the message should name
// what is lost; "verified" is reserved, because the possession check proves
// less than that word claims.
var forbidden = regexp.MustCompile(`(?i)\bmay\b|\bmight\b|\bpossibly\b|could potentially|data loss|\bverif(y|ied)\b|\x{2014}`)

// TestWordsNameWhatIsLost holds every message to the phase's wording rule:
// concrete nouns, present tense, no hedging, and never the word "verified".
func TestWordsNameWhatIsLost(t *testing.T) {
	for name, msg := range everyMessage() {
		if msg == "" {
			t.Errorf("%s is empty", name)
			continue
		}
		if m := forbidden.FindString(msg); m != "" {
			t.Errorf("%s uses %q:\n%s", name, m, msg)
		}
		if testing.Verbose() {
			t.Logf("---- %s ----\n%s", name, msg)
		}
	}
}

// TestRefusalsNameTheCountsAndTheWayOut pins the three facts each key
// refusal must carry: what the data is, how much of it, and what to do.
func TestRefusalsNameTheCountsAndTheWayOut(t *testing.T) {
	msgs := everyMessage()
	checks := map[string][]string{
		"first write refusal":     {"47 credentials", "the stored properties of 213 devices", "the survey answers of 1 saved launch configuration", "13 of these are encrypted under " + PublishedComposeKeyName, "permanently unreadable", "docker compose down --volumes"},
		"replace refusal":         {"47 credentials", "the stored properties of 200 devices", "does not override this", "ROTATE_ENCRYPTION_KEYS=true", "key rotation complete"},
		"replace without a count": {"no database is configured", "does not replace a key without counting first"},
		"already set up":          {"/srv/.env", "irreversible", "--destroy-existing-encryption-key"},
		"confirmation prompt":     {`"destroy `, "every backup of this database", "This cannot be undone", "3 encrypted items"},
	}
	for name, wants := range checks {
		for _, want := range wants {
			if !strings.Contains(msgs[name], want) {
				t.Errorf("%s does not say %q:\n%s", name, want, msgs[name])
			}
		}
	}
	if strings.Contains(msgs["replace refusal"], "mesh signing") {
		t.Error("a refusal listed a column that holds nothing")
	}
}

// TestOutageExplanationStatesTheDerivedNumbers proves the outage text quotes
// what topology derives rather than a hand-written claim, including the
// correction that the duplicate window stops at five minutes.
func TestOutageExplanationStatesTheDerivedNumbers(t *testing.T) {
	cases := map[time.Duration][]string{
		30 * time.Minute: {"7 days", "336 times", "last 5m of sends"},
		12 * time.Hour:   {"168 days", "336 times", "last 5m of sends", "does not grow"},
		time.Minute:      {"5h36m", "last 1m of sends"},
	}
	for budget, wants := range cases {
		text := OutageExplanation(topology.OutageBudget(budget))
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("for %s the explanation does not say %q:\n%s", budget, want, text)
			}
		}
		if !strings.Contains(text, "credentials") {
			t.Errorf("for %s the explanation does not say dispatch messages carry credentials", budget)
		}
	}
}

// TestPublishedComposeKey pins the published key to the value earlier
// compose files shipped, so the census recognizes rows written under it.
func TestPublishedComposeKey(t *testing.T) {
	if got := PublishedComposeKey(); string(got) != strings.Repeat("k", 32) {
		t.Fatalf("PublishedComposeKey() = %q, want 32 bytes of k", got)
	}
}
