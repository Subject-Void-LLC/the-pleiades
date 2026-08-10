// This file tests the controller's own configuration resolution from
// inside package main, since resolveDatabaseDSN is unexported and the
// decision it makes (which backend this process talks to) is not
// observable from outside without standing up two real servers.
package main

import (
	"strings"
	"testing"
)

// TestResolveDatabaseDSN covers every combination of the two database
// environment variables, including the one that is deliberately an error.
func TestResolveDatabaseDSN(t *testing.T) {
	tests := []struct {
		name    string
		dsn     string
		path    string
		want    string
		wantErr bool
	}{
		{
			name: "neither set falls back to the historical sqlite default",
			want: "sqlite://controller.db",
		},
		{
			name: "DB_PATH alone is the sqlite shorthand",
			path: "state/controller.db",
			want: "sqlite://state/controller.db",
		},
		{
			name: "DB_DSN alone names a postgres server",
			dsn:  "postgres://pleiades:password@postgres:5432/pleiades?sslmode=disable",
			want: "postgres://pleiades:password@postgres:5432/pleiades?sslmode=disable",
		},
		{
			name: "DB_DSN alone can also name sqlite explicitly",
			dsn:  "sqlite:///var/lib/pleiades/controller.db",
			want: "sqlite:///var/lib/pleiades/controller.db",
		},
		{
			// Both set is ambiguous, and guessing which one an operator
			// meant is how a deployment quietly runs on the wrong
			// database.
			name:    "both set is a startup error rather than a silent precedence rule",
			dsn:     "postgres://host/db",
			path:    "controller.db",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Setenv restores the previous value when the test ends, so
			// these cases cannot leak into one another.
			t.Setenv("DB_DSN", tc.dsn)
			t.Setenv("DB_PATH", tc.path)

			got, err := resolveDatabaseDSN()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveDatabaseDSN() = %q, want an error", got)
				}
				// The message has to name both variables, since that is
				// the only way an operator learns what to unset.
				for _, want := range []string{"DB_DSN", "DB_PATH"} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("the error %q does not name %s", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveDatabaseDSN() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("resolveDatabaseDSN() = %q, want %q", got, tc.want)
			}
		})
	}
}
