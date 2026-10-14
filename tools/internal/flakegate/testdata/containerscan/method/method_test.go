// Package method starts a container through a fixture's method.
package method

import (
	"testing"

	"example.com/scan/helper"
)

func TestMethod(t *testing.T) {
	var f helper.Fixture
	f.Boot()
}
