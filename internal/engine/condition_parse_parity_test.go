// Tests that a condition's references are read with exactly the syntax it
// compiles with: every condition the compile environment accepts parses for
// each reader that works out what it reads, so none is ever taken to read
// nothing because its reader could not parse it (FAILURE_PATTERNS 1351).
package engine_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// parityConditions are conditions using every piece of syntax the compile
// environment adds to plain CEL: the optional field and index, the optional
// macros, the standard macros, and each library's functions.
var parityConditions = []string{
	"device.?name.hasValue()",
	`device[?"name"].orValue("") == "web1"`,
	"vars.?a.optMap(v, v + 1).orValue(0) == 1",
	"vars.?a.optFlatMap(v, optional.of(v)).hasValue()",
	"optional.of(1).hasValue() && optional.none().orValue(2) == 2",
	"result.?probe.?rc.orValue(1) == 0",
	`nodes[?"probe"].hasValue()`,
	`stat.?b.value()[""].rc == 0`,
	`facts[?"os.family"].orValue("") == "Debian"`,
	"facts.?uptime.hasValue()",
	"error.?msg.hasValue()",
	"{'a': 1}.?a.hasValue()",
	"[1, 2].all(x, x > 0) && [1].exists(x, x == 1) && [1].map(x, x * 2)[0] == 2",
	"has(vars.a) && 'b' in vars",
	"ip('10.0.0.1').family() == 4 && cidr('10.0.0.0/8').containsIP(ip('10.1.2.3'))",
	"base64.encode(bytes('x')) == 'eA=='",
	"filters.safeInt(vars.a, 0) == 1",
}

// TestConditionParity_EveryConditionThatCompilesParsesForReferences holds
// each reader to the compile environment over parityConditions; the fuzz
// target below runs the same property over generated conditions.
func TestConditionParity_EveryConditionThatCompilesParsesForReferences(t *testing.T) {
	ev, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range parityConditions {
		// Control: each one really compiles, so the property is asked of it.
		if _, err := ev.Compile(src); err != nil {
			t.Fatalf("%q does not compile, so it proves nothing here: %v", src, err)
		}
		assertReadersParse(t, src)
	}
}

// FuzzConditionParity runs the parity property over generated conditions: any
// one that compiles must parse for every reader.
func FuzzConditionParity(f *testing.F) {
	for _, src := range parityConditions {
		f.Add(src)
	}
	ev, err := engine.NewCELEvaluator()
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if _, err := ev.Compile(src); err != nil {
			return
		}
		assertReadersParse(t, src)
	})
}

// assertReadersParse fails t when any reader of a condition's references
// cannot parse src, written as when_cel and as one when: item.
func assertReadersParse(t *testing.T, src string) {
	t.Helper()
	for _, c := range []engine.Conditional{{WhenCEL: src}, {When: engine.StringList{src}}} {
		if _, _, err := engine.ConditionReads(c); err != nil {
			t.Errorf("ConditionReads(%q): %v", src, err)
		}
		if _, err := engine.ConditionUsesRoot(c, engine.TemplateRootDevice); err != nil {
			t.Errorf("ConditionUsesRoot(%q): %v", src, err)
		}
		if _, err := engine.ConditionTestsPresence(c); err != nil {
			t.Errorf("ConditionTestsPresence(%q): %v", src, err)
		}
		if _, err := engine.ConditionFactReads(c); err != nil {
			t.Errorf("ConditionFactReads(%q): %v", src, err)
		}
		for _, s := range c.Sources() {
			if _, err := engine.ParseCondition(s); err != nil {
				t.Errorf("ParseCondition(%q): %v", s, err)
			}
		}
	}
}
