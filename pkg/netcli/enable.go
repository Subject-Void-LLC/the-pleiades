// Leaving a device's unprivileged exec mode with its enable secret.
package netcli

import (
	"context"
	"fmt"
	"regexp"
)

// enablePasswordPrompt is the question an enable command asks.
var enablePasswordPrompt = regexp.MustCompile(`(?im)^\s*password:\s*$`)

// maxEnableRetries bounds the empty lines sent to clear a device that asks
// for the secret again after refusing it (IOS asks three times).
const maxEnableRetries = 3

// enable sends the Dialect's Enable command, answers its password question
// with secret, and reports whether the device now sits at a privileged
// prompt. A refused secret is cleared with empty lines, so the session is
// left at its prompt, and is reported without the secret.
func (s *Session) enable(ctx context.Context, secret string) error {
	name := dialectName(s.dialect)
	either := regexp.MustCompile(enablePasswordPrompt.String() + `|` + s.dialect.Prompt(false).String())
	if err := s.shell.WriteLine(ctx, s.dialect.Enable); err != nil {
		return fmt.Errorf("netcli: sending %s's enable: %w", name, err)
	}
	got, err := s.shell.ReadUntil(ctx, either, s.maxBytes)
	if err != nil {
		return fmt.Errorf("netcli: reading %s's enable: %w", name, err)
	}
	if enablePasswordPrompt.MatchString(got) {
		if err := s.shell.WriteLine(ctx, secret); err != nil {
			return fmt.Errorf("netcli: answering %s's enable: %w", name, err)
		}
		for retry := 0; ; retry++ {
			if got, err = s.shell.ReadUntil(ctx, either, s.maxBytes); err != nil {
				return fmt.Errorf("netcli: reading %s's answer to the enable secret: %w", name, err)
			}
			if !enablePasswordPrompt.MatchString(got) {
				break
			}
			if retry == maxEnableRetries {
				return fmt.Errorf("netcli: %s kept asking for the enable secret", name)
			}
			if err := s.shell.WriteLine(ctx, ""); err != nil {
				return fmt.Errorf("netcli: clearing %s's enable question: %w", name, err)
			}
		}
	}
	if s.dialect.Unprivileged.MatchString(got) {
		return fmt.Errorf("netcli: %s refused the enable secret, so the session is still unprivileged", name)
	}
	s.unprivileged = false
	return nil
}
