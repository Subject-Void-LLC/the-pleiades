// Package project_test's credential-shape coverage: which credentials can
// authenticate a clone, and which only look as though they can.
package project_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// TestAuthenticatesGit is the rule a project's credential chooser and both
// of its write paths share.
//
// It asks what a credential CARRIES rather than what its type is CALLED, so
// a deployment's own type works and a cloud credential does not, without
// either being named here.
func TestAuthenticatesGit(t *testing.T) {
	const marker = "$encrypted$"

	for _, tc := range []struct {
		name   string
		inputs map[string]string
		want   bool
	}{
		{"a token", map[string]string{project.InputPassword: marker}, true},
		{"a username and password", map[string]string{
			project.InputUsername: "someone", project.InputPassword: marker,
		}, true},
		{"an ssh key", map[string]string{project.InputPrivateKey: marker}, true},
		{"an ssh key with a passphrase", map[string]string{
			project.InputPrivateKey: marker, project.InputPassphrase: marker,
		}, true},

		// A username alone is the case worth having a test for. Git will
		// attempt a clone with it, fail on the far side, and report
		// something about authentication that never says the credential
		// carried no secret.
		{"a username alone", map[string]string{project.InputUsername: "someone"}, false},
		{"nothing at all", map[string]string{}, false},
		{"a nil map", nil, false},

		// The shape a cloud credential has: real inputs, none of them
		// usable here.
		{"a cloud credential", map[string]string{
			"api_token": marker, "api_url": "https://api.example.test",
		}, false},

		// Present but empty is not supplied. credstore writes a marker for
		// a secret it holds, so an empty string means the credential does
		// not carry that input rather than that it carries a blank one.
		{"an empty password", map[string]string{project.InputPassword: ""}, false},
		{"an empty key", map[string]string{project.InputPrivateKey: ""}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := project.AuthenticatesGit(tc.inputs); got != tc.want {
				t.Errorf("AuthenticatesGit(%v) = %v, want %v", tc.inputs, got, tc.want)
			}
		})
	}
}
