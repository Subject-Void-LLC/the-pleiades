package templates

import (
	"net/url"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This stage's Schema and Injection Hardening check for the launch form's
// own new boundary: a submission naming a credential input.
//
// The control name carries a credential id, and a submission is attacker
// controlled. The question that matters is whether somebody can post
// credential_<other id>_<input> and have a value delivered to a credential
// the template does not bind, or to an input the type does not declare.

// promptValues builds a submission the way the form machinery delivers
// one: through view.NewValues, narrowed against the fields the form
// actually rendered, which is what a real request goes through.
//
// It returns the undeclared keys too, because those are half the answer.
// A control name the form never offered is not silently dropped; it is
// reported, and the write is refused rather than proceeding with a
// submission carrying something nobody declared.
func promptValues(rendered []view.Field, pairs map[string]string) (view.Values, []string) {
	form := url.Values{}
	for k, v := range pairs {
		form.Set(k, v)
	}
	return view.NewValues(rendered, form, false)
}

// TestBindPromptedCredentialsReadsOnlyTheControlsItRendered is the
// security property, and it holds twice over.
//
// The shared form machinery narrows a submission to the controls the
// descriptor offered, reporting the rest as undeclared, before any code in
// this package sees it. Then bindPromptedCredentials iterates the fields
// the form rendered and reads each one out of the submission, rather than
// iterating the submission and trusting the names in it. So a value for a
// credential this template does not bind is not filtered out; there is
// nowhere for it to be read from.
func TestBindPromptedCredentialsReadsOnlyTheControlsItRendered(t *testing.T) {
	t.Parallel()

	rendered := []view.Field{
		{Name: credentialControl(7, "one_time_code"), Kind: view.KindPassword, InForm: true, Label: "CODE"},
	}

	values, undeclared := promptValues(rendered, map[string]string{
		// The control the form offered.
		credentialControl(7, "one_time_code"): "typed-at-launch",
		// A credential this template does not bind.
		credentialControl(99, "one_time_code"): "somebody-elses",
		// An input the bound credential's type does not declare.
		credentialControl(7, "not_declared"): "invented",
		// A survey answer, which shares the form and must not be mistaken
		// for a credential input.
		"answer_one_time_code": "a survey answer",
		// Something shaped like the prefix but not a control.
		"credential_": "malformed",
	})

	if len(undeclared) != 4 {
		t.Errorf("undeclared = %v, want the four names the form never rendered", undeclared)
	}

	got := bindPromptedCredentials(rendered, values)

	if len(got) != 1 {
		t.Fatalf("bound %d credential(s), want only the one the form rendered: %v", len(got), got)
	}
	if got[7]["one_time_code"] != "typed-at-launch" {
		t.Errorf("the rendered control's value = %q, want it delivered", got[7]["one_time_code"])
	}
	if _, leaked := got[99]; leaked {
		t.Error("a submission delivered a value to a credential this template does not bind")
	}
	if _, invented := got[7]["not_declared"]; invented {
		t.Error("a submission delivered a value to an input the type does not declare")
	}
}

// TestAnEmptyPromptIsAbsentRatherThanEmpty covers the distinction the
// injector depends on. Absent means "not answered", which is reported by
// name; an empty string would mean "answered with nothing", which injects
// a blank secret and fails against the remote service instead.
func TestAnEmptyPromptIsAbsentRatherThanEmpty(t *testing.T) {
	t.Parallel()

	rendered := []view.Field{
		{Name: credentialControl(7, "one_time_code"), Kind: view.KindPassword, InForm: true, Label: "CODE"},
	}

	values, _ := promptValues(rendered, map[string]string{
		credentialControl(7, "one_time_code"): "",
	})

	if got := bindPromptedCredentials(rendered, values); len(got) != 0 {
		t.Errorf("an unanswered prompt produced %v, want nothing at all", got)
	}
}

// TestParseCredentialControlSplitsOnTheFirstUnderscoreAfterTheID pins the
// parsing rule, because the obvious alternative is wrong in a way that is
// hard to see: an input id legitimately contains underscores, so splitting
// on the last one turns ssh_key_unlock into ssh_key.
func TestParseCredentialControlSplitsOnTheFirstUnderscoreAfterTheID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		control string
		id      int
		input   string
		ok      bool
	}{
		{"an ordinary input", "credential_3_token", 3, "token", true},
		{"an input id carrying underscores", "credential_3_ssh_key_unlock", 3, "ssh_key_unlock", true},
		{"a multi-digit credential id", "credential_1024_token", 1024, "token", true},
		{"no prefix at all", "answer_token", 0, "", false},
		{"the prefix and nothing else", "credential_", 0, "", false},
		{"an id that is not a number", "credential_abc_token", 0, "", false},
		{"a zero id, which no credential has", "credential_0_token", 0, "", false},
		{"a negative id", "credential_-1_token", 0, "", false},
		{"an id with no input after it", "credential_3_", 0, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			id, input, ok := parseCredentialControl(tt.control)
			if ok != tt.ok {
				t.Fatalf("parseCredentialControl(%q) ok = %v, want %v", tt.control, ok, tt.ok)
			}
			if !ok {
				return
			}
			if id != tt.id || input != tt.input {
				t.Errorf("parseCredentialControl(%q) = (%d, %q), want (%d, %q)", tt.control, id, input, tt.id, tt.input)
			}
		})
	}
}
