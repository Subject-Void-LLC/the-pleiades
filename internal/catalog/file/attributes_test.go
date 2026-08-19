package file

import "testing"

// This file is an internal test (package file, not file_test) because the
// rules it covers are unexported and shared by every method in the
// namespace. Reaching them through one method's exported entry point
// would test that method's plumbing as much as the rule, and would leave
// the rule covered only for whichever methods happened to exercise it.

func TestTextParam(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		want    string
		wantErr bool
	}{
		{name: "absent", params: map[string]any{}, want: ""},
		{name: "explicit null", params: map[string]any{"mode": nil}, want: ""},
		{name: "a string", params: map[string]any{"mode": "0644"}, want: "0644"},
		{name: "the empty string is a value, not an absence", params: map[string]any{"content": ""}, want: ""},
		// The whole reason this exists instead of sdk.StringParam: YAML
		// turns an unquoted 0644 into the number 420, and reporting that
		// as absent silently drops the mode.
		{name: "an int", params: map[string]any{"mode": 420}, wantErr: true},
		// The shape the same value can arrive in after crossing the
		// runner's task subprocess boundary.
		{name: "a float", params: map[string]any{"mode": 420.0}, wantErr: true},
		{name: "a bool", params: map[string]any{"owner": true}, wantErr: true},
		{name: "a list", params: map[string]any{"group": []any{"a"}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "mode"
			for k := range tt.params {
				key = k
			}
			got, err := textParam(tt.params, key)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("textParam(%v) returned no error", tt.params)
				}
				return
			}
			if err != nil {
				t.Fatalf("textParam(%v): %v", tt.params, err)
			}
			if got != tt.want {
				t.Errorf("textParam(%v) = %q, want %q", tt.params, got, tt.want)
			}
		})
	}
}

func TestCheckMode(t *testing.T) {
	for _, mode := range []string{"", "0", "644", "0644", "4755", "7777"} {
		if err := checkMode("mode", mode); err != nil {
			t.Errorf("checkMode(%q) refused a mode it should accept: %v", mode, err)
		}
	}
	// A symbolic mode cannot be compared against what stat reports, and
	// an eight or nine is not an octal digit. Both would otherwise reach
	// chmod on the device and produce a task that never converges.
	for _, mode := range []string{"u+x", "go-w", "08", "0649", "07777", "0644 ", "-644"} {
		if err := checkMode("mode", mode); err == nil {
			t.Errorf("checkMode(%q) accepted a mode this namespace cannot compare", mode)
		}
	}
}

func TestCheckName(t *testing.T) {
	for _, name := range []string{"", "root", "app", "user1", "1000abc"} {
		if err := checkName("owner", name); err != nil {
			t.Errorf("checkName(%q) refused a usable account name: %v", name, err)
		}
	}
	// chown reads an all-digit argument as an id while stat reports
	// names, so these would compare unequal on every run.
	for _, name := range []string{"0", "1000", "65534"} {
		if err := checkName("owner", name); err == nil {
			t.Errorf("checkName(%q) accepted a numeric id", name)
		}
	}
}

// TestDigitsOnly covers the shared predicate directly, including the
// empty-string case its callers happen to guard against today.
//
// That case is worth pinning precisely because nothing reaches it right
// now. Without the guard the loop would run zero times and return true,
// so "" would read as a valid mode and as a numeric id, and the only
// thing standing between that and a wrong answer is that both callers
// currently check for the empty string first. A future caller that does
// not is the bug this guard prevents, and an untested guard is one
// somebody deletes as dead code.
func TestDigitsOnly(t *testing.T) {
	tests := []struct {
		text string
		max  byte
		want bool
	}{
		{text: "", max: '7', want: false},
		{text: "", max: '9', want: false},
		{text: "0644", max: '7', want: true},
		{text: "0648", max: '7', want: false},
		{text: "1000", max: '9', want: true},
		{text: "10a0", max: '9', want: false},
		{text: "9", max: '9', want: true},
		{text: "9", max: '7', want: false},
	}
	for _, tt := range tests {
		if got := digitsOnly(tt.text, tt.max); got != tt.want {
			t.Errorf("digitsOnly(%q, %q) = %v, want %v", tt.text, tt.max, got, tt.want)
		}
	}
}

func TestIsOctalRejectsTooManyDigits(t *testing.T) {
	if !isOctal("4755") {
		t.Error("isOctal rejected the widest real mode")
	}
	if isOctal("04755") {
		t.Error("isOctal accepted five digits, which is not a mode anyone meant")
	}
}

// TestAttributeParams covers the reader the methods actually call,
// including that a refusal on any one of the three stops the whole read.
func TestAttributeParams(t *testing.T) {
	t.Run("all three", func(t *testing.T) {
		got, err := attributeParams(map[string]any{"mode": "0644", "owner": "app", "group": "web"}, "mode", "owner", "group")
		if err != nil {
			t.Fatalf("attributeParams: %v", err)
		}
		if got.Mode != "0644" || got.Owner != "app" || got.Group != "web" {
			t.Errorf("attributeParams = %+v, want mode 0644, owner app, group web", got)
		}
	})

	t.Run("absent fields stay empty so Apply leaves them alone", func(t *testing.T) {
		got, err := attributeParams(map[string]any{"mode": "0644"}, "mode", "owner", "group")
		if err != nil {
			t.Fatalf("attributeParams: %v", err)
		}
		if got.Owner != "" || got.Group != "" {
			t.Errorf("attributeParams = %+v, want owner and group empty", got)
		}
	})

	// One case per parameter, because each is read and checked by its own
	// call and a copy-paste mistake in any of them would leave exactly
	// one unvalidated.
	refusals := []struct {
		name   string
		params map[string]any
	}{
		{name: "a mode that is not text", params: map[string]any{"mode": 384}},
		{name: "an owner that is not text", params: map[string]any{"owner": 1000}},
		{name: "a group that is not text", params: map[string]any{"group": 1000}},
		{name: "a symbolic mode", params: map[string]any{"mode": "u+x"}},
		{name: "a numeric owner", params: map[string]any{"owner": "1000"}},
		{name: "a numeric group", params: map[string]any{"group": "1000"}},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			got, err := attributeParams(tt.params, "mode", "owner", "group")
			if err == nil {
				t.Fatalf("attributeParams(%v) returned no error", tt.params)
			}
			if got.Mode != "" || got.Owner != "" || got.Group != "" {
				t.Errorf("a refused read returned %+v, want the zero value: a partial result invites a caller to use it", got)
			}
		})
	}
}
