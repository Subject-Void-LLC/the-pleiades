// Package helper is a fixture for the container scan: helpers that start a
// container directly, through each other and through a method, and ones
// that only look at the daemon.
package helper

import (
	"os/exec"
	"testing"

	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Start creates a container.
func Start(t *testing.T) { _, _ = tc.GenericContainer(nil, tc.GenericContainerRequest{}) }

// Indirect starts one through Start.
func Indirect(t *testing.T) { Start(t) }

// Net creates a network.
func Net() { _, _ = network.New(nil) }

// Build builds an image with the docker command.
func Build() { _ = exec.Command("docker", "--debug", "build", ".").Run() }

// Info only asks the daemon.
func Info() { _ = exec.Command("docker", "info").Run() }

// Strategy only names a wait strategy.
func Strategy() any { return wait.ForLog("ready") }

// Require starts nothing.
func Require(t *testing.T) { t.Helper() }

// Fixture is a container a method starts.
type Fixture struct{}

// Boot starts a container.
func (*Fixture) Boot() { Indirect(nil) }

// Peek starts nothing.
func (Fixture) Peek() {}
