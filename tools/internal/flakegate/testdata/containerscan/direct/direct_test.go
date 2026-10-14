// Package direct imports testcontainers-go in its tests.
package direct

import (
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

func TestDirect(t *testing.T) { _ = testcontainers.ContainerRequest{} }
