// Package transitive starts a container through a helper that calls
// another.
package transitive

import (
	"testing"

	"example.com/scan/helper"
)

func TestTransitive(t *testing.T) { helper.Indirect(t) }
