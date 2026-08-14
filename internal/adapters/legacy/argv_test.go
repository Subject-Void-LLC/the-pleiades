package legacy

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestBuildArgv proves every field AWX_PARITY_ROADMAP.md Section 3b.1
// names (verbosity, limit, forks, job_tags, skip_tags, extra_vars) reaches
// the real ansible-playbook argv at a specific position, table-driven per
// .AGENTS/AGENTS.md's own convention, and that an absent field is omitted
// entirely rather than emitted with a zero-ish value.
//
// The extra-variables rows changed shape in Phase 22: `-e <json>` became
// `-e @/run/pleiades/extravars.json`, unconditionally. That is the argv
// leak fix, and buildArgv's own doc comment carries the reasoning.
func TestBuildArgv(t *testing.T) {
	tests := []struct {
		name         string
		fields       launch.Fields
		hasExtraVars bool
		vaults       []wire.InjectedVault
		want         []string
	}{
		{
			name:   "no fields set produces the bare invocation",
			fields: nil,
			want:   []string{"ansible-playbook", "-i", "/inv.json", "/pb.yml"},
		},
		{
			name:   "verbosity 1 through 4 map onto -v through -vvvv",
			fields: launch.Fields{"verbosity": 3},
			want:   []string{"ansible-playbook", "-vvv", "-i", "/inv.json", "/pb.yml"},
		},
		{
			name:   "verbosity above 4 clamps to -vvvv rather than over-escalating",
			fields: launch.Fields{"verbosity": 9},
			want:   []string{"ansible-playbook", "-vvvv", "-i", "/inv.json", "/pb.yml"},
		},
		{
			name:   "verbosity 0 emits no -v flag at all",
			fields: launch.Fields{"verbosity": 0},
			want:   []string{"ansible-playbook", "-i", "/inv.json", "/pb.yml"},
		},
		{
			name:   "limit reaches --limit",
			fields: launch.Fields{"limit": "core-switch-1"},
			want:   []string{"ansible-playbook", "-i", "/inv.json", "--limit", "core-switch-1", "/pb.yml"},
		},
		{
			name:   "forks reaches --forks",
			fields: launch.Fields{"forks": 1},
			want:   []string{"ansible-playbook", "-i", "/inv.json", "--forks", "1", "/pb.yml"},
		},
		{
			name:   "forks 0 (unset) emits no --forks flag, letting ansible-playbook default apply",
			fields: launch.Fields{"forks": 0},
			want:   []string{"ansible-playbook", "-i", "/inv.json", "/pb.yml"},
		},
		{
			name:   "job_tags reaches --tags, comma-joined",
			fields: launch.Fields{"job_tags": []string{"deploy", "restart"}},
			want:   []string{"ansible-playbook", "-i", "/inv.json", "--tags", "deploy,restart", "/pb.yml"},
		},
		{
			name:   "skip_tags reaches --skip-tags, comma-joined",
			fields: launch.Fields{"skip_tags": []string{"slow"}},
			want:   []string{"ansible-playbook", "-i", "/inv.json", "--skip-tags", "slow", "/pb.yml"},
		},
		{
			name:         "extra vars reach -e as a FILE reference, never a value",
			hasExtraVars: true,
			want: []string{
				"ansible-playbook", "-i", "/inv.json", "/pb.yml",
				"-e", "@/run/pleiades/extravars.json",
			},
		},
		{
			name:         "no extra vars emits no -e at all",
			hasExtraVars: false,
			want:         []string{"ansible-playbook", "-i", "/inv.json", "/pb.yml"},
		},
		{
			name:   "a named vault identity reaches --vault-id as label@path",
			vaults: []wire.InjectedVault{{Identifier: "prod", Path: "/run/pleiades/credentials/9.vault"}},
			want: []string{
				"ansible-playbook", "-i", "/inv.json",
				"--vault-id", "prod@/run/pleiades/credentials/9.vault", "/pb.yml",
			},
		},
		{
			name:   "the unnamed default identity reaches --vault-id as a bare path",
			vaults: []wire.InjectedVault{{Path: "/run/pleiades/credentials/9.vault"}},
			want: []string{
				"ansible-playbook", "-i", "/inv.json",
				"--vault-id", "/run/pleiades/credentials/9.vault", "/pb.yml",
			},
		},
		{
			name: "two vault identities each get their own flag, in order",
			vaults: []wire.InjectedVault{
				{Identifier: "prod", Path: "/run/pleiades/credentials/9.vault"},
				{Identifier: "staging", Path: "/run/pleiades/credentials/10.vault"},
			},
			want: []string{
				"ansible-playbook", "-i", "/inv.json",
				"--vault-id", "prod@/run/pleiades/credentials/9.vault",
				"--vault-id", "staging@/run/pleiades/credentials/10.vault",
				"/pb.yml",
			},
		},
		{
			name: "every field at once, in the documented order",
			fields: launch.Fields{
				"verbosity": 1,
				"limit":     "core-switch-1",
				"forks":     1,
				"job_tags":  []string{"deploy"},
				"skip_tags": []string{"slow"},
			},
			hasExtraVars: true,
			vaults:       []wire.InjectedVault{{Identifier: "prod", Path: "/run/pleiades/credentials/9.vault"}},
			want: []string{
				"ansible-playbook", "-v", "-i", "/inv.json",
				"--limit", "core-switch-1", "--forks", "1", "--tags", "deploy", "--skip-tags", "slow",
				"--vault-id", "prod@/run/pleiades/credentials/9.vault",
				"/pb.yml", "-e", "@/run/pleiades/extravars.json",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildArgv(tt.fields, tt.hasExtraVars, tt.vaults, "/inv.json", "/pb.yml")
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildArgv() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// TestBuildArgvNamesTheFileItsWriterWrites is what stops the two halves of
// the extra-variables path from drifting.
//
// The path appears in exactly one place (extraVarsContainerPath) and both
// the argv flag and the ContainerFile read it, so a change to one is a
// change to both. This asserts the flag really is built from that constant
// rather than from a literal that happens to match today.
func TestBuildArgvNamesTheFileItsWriterWrites(t *testing.T) {
	argv := buildArgv(nil, true, nil, "/inv.json", "/pb.yml")

	last := argv[len(argv)-1]
	if last != "@"+extraVarsContainerPath {
		t.Fatalf("the -e argument is %q, want @%s", last, extraVarsContainerPath)
	}
	if argv[len(argv)-2] != "-e" {
		t.Fatalf("argv = %#v, want -e immediately before the file reference", argv)
	}
}

// TestExtraVarsAreEncodedAsStrictJSON covers the choice of encoding, which
// is not cosmetic.
//
// ansible-core accepts either JSON or YAML in an @-file, and YAML's own
// scalar rules would silently reinterpret several ordinary values: "yes"
// becomes a boolean, "1.20" becomes a float that no longer round-trips as
// the version string somebody typed. JSON has no ambiguous scalars, so an
// extra variable means exactly what the launch said it meant.
func TestExtraVarsAreEncodedAsStrictJSON(t *testing.T) {
	encoded, err := encodeExtraVars(map[string]any{
		"ambiguous_bool":    "yes",
		"ambiguous_version": "1.20",
		"real_bool":         true,
	})
	if err != nil {
		t.Fatalf("encodeExtraVars() error = %v", err)
	}

	body := string(encoded)
	for _, want := range []string{`"ambiguous_bool":"yes"`, `"ambiguous_version":"1.20"`, `"real_bool":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("the encoded extra vars do not contain %s: %s", want, body)
		}
	}
}

// TestRunTimeout proves runTimeout reads the "timeout" field's own
// documented semantics: seconds, converted to a time.Duration, and zero
// (whether from an absent field or an explicit 0) means "no timeout,"
// never a zero-length deadline that would abandon the run instantly.
func TestRunTimeout(t *testing.T) {
	tests := []struct {
		name   string
		fields launch.Fields
		want   time.Duration
	}{
		{name: "unset field", fields: nil, want: 0},
		{name: "explicit zero", fields: launch.Fields{"timeout": 0}, want: 0},
		{name: "30 seconds", fields: launch.Fields{"timeout": 30}, want: 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runTimeout(tt.fields); got != tt.want {
				t.Errorf("runTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}
