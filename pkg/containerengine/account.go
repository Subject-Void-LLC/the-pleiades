// The checks a session makes before its first request beyond the engine's
// identity: the Engine API version floor, and the account it logs in as.
package containerengine

import (
	"context"
	"fmt"
	"os/user"
	"strings"

	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/versions"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// apiFloor is the oldest Engine API version a session accepts from each
// engine. Below it a session is refused, naming the version, rather than
// sent requests the engine reads differently.
//
// Both are 1.41, measured 2026-10-14 against the releases this platform is
// tested on and the oldest one the long-term distributions ship: Docker
// 29.9.0 serves 1.56 (and down to 1.40), Podman 5.8.7's compatible API serves
// 1.44 and Podman 4.9.4's (Ubuntu 24.04's) serves 1.41, each down to 1.24.
// 1.41 is also Docker 20.10's. Docker's own client refuses below 1.40 on its
// own, so the floor is ours to state, not the client's.
var apiFloor = map[capability.EngineKind]string{
	capability.EngineDocker: "1.41",
	capability.EnginePodman: "1.41",
}

// upgradeHint names the oldest release of each engine that clears its floor.
var upgradeHint = map[capability.EngineKind]string{
	capability.EngineDocker: "Docker 20.10 or later",
	capability.EnginePodman: "Podman 4.0 or later",
}

// negotiate fixes the session's API version: the engine's own, or the
// client's newest when the engine is newer, refusing an engine below its
// floor. It runs once per session, before the caller's first request.
func negotiate(ctx context.Context, api *client.Client, kind capability.EngineKind, release string) (string, error) {
	ping, err := api.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})
	// The client reports an engine below its own minimum as an error after
	// reading the version, so the version is judged first: the refusal a
	// reader needs names the engine's version and this platform's floor.
	floor := apiFloor[kind]
	if ping.APIVersion != "" && versions.LessThan(ping.APIVersion, floor) {
		return "", fmt.Errorf("it serves Engine API %s (release %s), below the %s this platform reaches it at; upgrade it to %s",
			ping.APIVersion, release, floor, upgradeHint[kind])
	}
	if err != nil {
		return "", fmt.Errorf("asking its API version: %w", err)
	}
	// An engine that names no version would be spoken to at the client's
	// newest, which is a guess about an engine that did not say.
	if ping.APIVersion == "" {
		return "", fmt.Errorf("it named no Engine API version, so which requests it reads as this platform means cannot be known")
	}
	return api.ClientVersion(), nil
}

// LocalUser returns the account this process runs as. Onboarding records it
// for an engine on this machine, and a session compares it, so both ask the
// one question the same way.
func LocalUser() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("containerengine: which account this process runs as: %w", err)
	}
	return u.Username, nil
}

// remoteUser asks a device which account conn logged in as: `id -un`, the
// question onboarding's probe asked, so the two answers compare.
func remoteUser(ctx context.Context, conn *remoteexec.Conn) (string, error) {
	res, err := conn.Run(ctx, "id -un")
	if err != nil {
		return "", fmt.Errorf("asking which account this login is: %w", err)
	}
	who := strings.TrimSpace(res.Stdout)
	if res.ExitCode != 0 || who == "" {
		return "", fmt.Errorf("asking which account this login is: `id -un` exited %d: %s", res.ExitCode, farside.Output(res.Stderr))
	}
	return who, nil
}

// sameAccount refuses a session whose account is not the one onboarding
// probed the engine as. The facts a session relies on are that account's: a
// rootless engine and its socket belong to one user, and whether a login may
// open a rootful engine's socket depends on its groups.
func sameAccount(fqcn, device string, kind capability.EngineKind, recorded, now string) error {
	if recorded == "" {
		return fmt.Errorf("%s: device %q records no account its %s engine was discovered as; run `pleiades onboard %s`", fqcn, device, kind, device)
	}
	if recorded != now {
		return fmt.Errorf("%s: device %q's %s engine was discovered logged in as %q, and this run is %q, whose access to it may differ; run `pleiades onboard %s` with the credential this run uses",
			fqcn, device, kind, recorded, now, device)
	}
	return nil
}
