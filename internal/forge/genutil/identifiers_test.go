package genutil_test

import (
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/genutil"
)

func TestValidateSegment(t *testing.T) {
	tests := []struct {
		name    string
		segment string
		wantErr bool
	}{
		{name: "valid short", segment: "cisco", wantErr: false},
		{name: "valid with digits and underscore", segment: "junos_router9", wantErr: false},
		{name: "valid single letter", segment: "a", wantErr: false},
		{name: "empty", segment: "", wantErr: true},
		{name: "leading digit", segment: "3com", wantErr: true},
		{name: "leading digit all numeric", segment: "0day", wantErr: true},
		{name: "uppercase", segment: "Cisco", wantErr: true},
		{name: "embedded dot", segment: "foo.bar", wantErr: true},
		{name: "embedded slash", segment: "foo/bar", wantErr: true},
		{name: "embedded backslash", segment: "foo\\bar", wantErr: true},
		{name: "path traversal", segment: "..", wantErr: true},
		{name: "embedded space", segment: "foo bar", wantErr: true},
		{name: "go keyword func", segment: "func", wantErr: true},
		{name: "go keyword type", segment: "type", wantErr: true},
		{name: "go keyword package", segment: "package", wantErr: true},
		{name: "go keyword for", segment: "for", wantErr: true},
		{name: "predeclared identifier true is allowed", segment: "true", wantErr: false},
		{name: "over length", segment: strings.Repeat("a", 65), wantErr: true},
		{name: "max length exactly", segment: strings.Repeat("a", 64), wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := genutil.ValidateSegment(tt.segment)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateSegment(%q) error = %v, wantErr %v", tt.segment, err, tt.wantErr)
			}
		})
	}
}

func TestValidateSegments(t *testing.T) {
	tests := []struct {
		name     string
		segments []string
		wantErr  bool
	}{
		{name: "single valid", segments: []string{"pkg"}, wantErr: false},
		{name: "multiple valid", segments: []string{"pkg", "apt", "install"}, wantErr: false},
		{name: "empty list", segments: nil, wantErr: true},
		{name: "one invalid among valid", segments: []string{"pkg", "..", "install"}, wantErr: true},
		{name: "too many segments", segments: make([]string, 17), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segments := tt.segments
			if tt.name == "too many segments" {
				segments = make([]string, 17)
				for i := range segments {
					segments[i] = "a"
				}
			}
			err := genutil.ValidateSegments(segments)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateSegments(%v) error = %v, wantErr %v", segments, err, tt.wantErr)
			}
		})
	}
}

func TestToExportedIdent(t *testing.T) {
	tests := []struct {
		snake string
		want  string
	}{
		{snake: "router", want: "Router"},
		{snake: "daemon_reload", want: "DaemonReload"},
		{snake: "install", want: "Install"},
		{snake: "a", want: "A"},
		{snake: "junos_router9", want: "JunosRouter9"},
	}

	for _, tt := range tests {
		t.Run(tt.snake, func(t *testing.T) {
			got := genutil.ToExportedIdent(tt.snake)
			if got != tt.want {
				t.Fatalf("ToExportedIdent(%q) = %q, want %q", tt.snake, got, tt.want)
			}
		})
	}
}
