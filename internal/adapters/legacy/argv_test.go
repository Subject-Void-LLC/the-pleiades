package legacy

import (
	"reflect"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// TestBuildArgv proves every field AWX_PARITY_ROADMAP.md Section 3b.1
// names (verbosity, limit, forks, job_tags, skip_tags, extra_vars) reaches
// the real ansible-playbook argv at a specific position, table-driven per
// .AGENTS/AGENTS.md's own convention, and that an absent field is omitted
// entirely rather than emitted with a zero-ish value.
func TestBuildArgv(t *testing.T) {
	tests := []struct {
		name      string
		fields    launch.Fields
		extraVars map[string]any
		want      []string
	}{
		{
			name:   "no fields set produces the old bare invocation, minus the hardcoded -v",
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
			name:      "extra_vars reaches -e as a trailing JSON object",
			extraVars: map[string]any{"deploy_env": "prod"},
			want:      []string{"ansible-playbook", "-i", "/inv.json", "/pb.yml", "-e", `{"deploy_env":"prod"}`},
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
			extraVars: map[string]any{"deploy_env": "prod"},
			want: []string{
				"ansible-playbook", "-v", "-i", "/inv.json",
				"--limit", "core-switch-1", "--forks", "1", "--tags", "deploy", "--skip-tags", "slow",
				"/pb.yml", "-e", `{"deploy_env":"prod"}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildArgv(tt.fields, tt.extraVars, "/inv.json", "/pb.yml")
			if err != nil {
				t.Fatalf("buildArgv returned unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildArgv() = %#v, want %#v", got, tt.want)
			}
		})
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
