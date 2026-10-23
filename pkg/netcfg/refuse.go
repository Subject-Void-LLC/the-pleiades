// Package netcfg: the refusal every method applying configuration text
// makes for text that holds a masked secret.
package netcfg

import (
	"fmt"
	"strings"
)

// RefuseMasked returns an error when text holds a placeholder Mask wrote,
// and nil otherwise. A configuration read through net.frr.facts, net.ios.*
// or net.batfish.check has its secrets masked; applied as it is, it would
// set every key and password to its placeholder and drop every
// authenticated session. A method applying configuration calls this
// before it reaches the device, so the refusal says nothing was sent. what
// names the text in the method's own terms ("the candidate", "config").
func RefuseMasked(what, text string) error {
	if !strings.Contains(text, MaskPrefix) {
		return nil
	}
	return fmt.Errorf("%s holds a masked secret (%s...), so it is a configuration as read, not one to apply; nothing was sent. Put each secret back first", what, MaskPrefix)
}
