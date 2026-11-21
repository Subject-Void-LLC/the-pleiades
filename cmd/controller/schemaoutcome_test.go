// Tests that the startup log says when this start applied a contract, since
// that is the moment an operator's rollback stops being a binary swap.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/migrate"
)

// TestLogSchemaOutcome_SaysWhenAContractWasApplied: an outcome naming a
// contract logs one warning with the contract and its floor; one naming none
// logs no such warning.
func TestLogSchemaOutcome_SaysWhenAContractWasApplied(t *testing.T) {
	cases := []struct {
		name    string
		outcome migrate.Outcome
		warns   bool
	}{
		{"a contract applied", migrate.Outcome{
			Applied:      []string{"0051_mesh_runner_seed.sql", "0052_fact_at_rest.sql"},
			Contracts:    []string{"0052_fact_at_rest.sql"},
			AppliedFloor: "0052_fact_at_rest.sql",
		}, true},
		{"only expand migrations applied", migrate.Outcome{
			Applied:      []string{"0051_mesh_runner_seed.sql"},
			AppliedFloor: "0032_add_launchables.sql",
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logSchemaOutcome(context.Background(), slog.New(slog.NewJSONHandler(&buf, nil)), tc.outcome)
			found := false
			for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
				var entry struct {
					Level     string   `json:"level"`
					Msg       string   `json:"msg"`
					Contracts []string `json:"contracts"`
					Floor     string   `json:"floor"`
				}
				if err := json.Unmarshal([]byte(line), &entry); err != nil {
					t.Fatalf("a log line is not JSON: %q: %v", line, err)
				}
				if !strings.Contains(entry.Msg, "contract migration") {
					continue
				}
				found = true
				if entry.Level != "WARN" || len(entry.Contracts) != 1 || entry.Contracts[0] != "0052_fact_at_rest.sql" || entry.Floor != "0052_fact_at_rest.sql" {
					t.Errorf("the contract line = %+v; want a warning naming 0052_fact_at_rest.sql and its floor", entry)
				}
			}
			if found != tc.warns {
				t.Errorf("logged a contract warning = %t, want %t:\n%s", found, tc.warns, buf.String())
			}
		})
	}
}
