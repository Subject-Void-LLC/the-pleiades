// Package external_test builds an image through an aliased import.
package external_test

import (
	"testing"

	h "example.com/scan/helper"
)

func TestExternal(t *testing.T) { h.Build() }
