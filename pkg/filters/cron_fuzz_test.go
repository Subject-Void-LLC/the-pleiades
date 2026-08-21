package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// FuzzIsValidCronExpr proves the hand-rolled cron parser never panics on
// arbitrary input, per this phase's own Fuzz/Stress Test checklist item
// to fuzz the cron parser against malformed input.
func FuzzIsValidCronExpr(f *testing.F) {
	seeds := []string{
		"* * * * *", "*/15 * * * *", "0 9-17 * * *", "0,15,30,45 * * * *",
		"", "* * * *", "* * * * * *", "60 * * * *", "*/0 * * * *",
		"17-9 * * * *", "@daily", "* * * jan *",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, expr string) {
		filters.IsValidCronExpr(expr)
	})
}
