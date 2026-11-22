// Tests for the launch form's mapping of what a launch returned onto the
// form: each refusal a person can act on lands on a control with a message,
// and anything else stays an error.
package templates

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
)

// TestLaunchRefusal_PutsEachRefusalOnTheForm walks every refusal the form
// names. The two that come from the runbook itself are built by the code
// that raises them: a runbook that does not compile, and one whose condition
// reads a stored fact, which used to render as a fault.
func TestLaunchRefusal_PutsEachRefusalOnTheForm(t *testing.T) {
	notCompiled := fmt.Errorf("resolve: %w", &runbook.CompileError{ID: "patch", Err: errors.New(`handler "restart": cannot take the lock`)})
	factReads := dispatch.RefuseFactReads([]engine.FactRead{{Node: "tasks[1]", Label: `tasks[1] (name "on debian")`, Keys: []string{"os.family"}}})
	if factReads == nil {
		t.Fatal("RefuseFactReads refused nothing")
	}

	for name, tc := range map[string]struct {
		err   error
		field string
		want  string
	}{
		"the template is gone":            {launch.ErrNotFound, "", "no longer exists"},
		"a survey answer":                 {fmt.Errorf("%w: version is required", launch.ErrSurveyAnswer), "", "version is required"},
		"a kind nothing runs":             {launch.ErrUnknownKind, "", "cannot run that kind"},
		"a mode":                          {fmt.Errorf("%w: a check cannot become a run", launch.ErrMode), launch.ModeField, "a check cannot become a run"},
		"prompted inputs with forks":      {api.ErrWindowedPrompt, dispatch.ForksField, "cannot also set forks"},
		"prompted inputs with serial":     {api.ErrSerialPrompt, "", "serial: batches"},
		"a runbook seeding a login":       {fmt.Errorf("%w: task a", dispatch.ErrSeededLogin), "", "seeds a device's login"},
		"a runbook reading a fact":        {factReads, "", "os.family"},
		"a runbook that does not compile": {notCompiled, "", `runbook "patch" does not compile: handler "restart"`},
	} {
		t.Run(name, func(t *testing.T) {
			errs, err := launchRefusal(tc.err)
			if err != nil {
				t.Fatalf("launchRefusal = fault %v, want a refusal on the form", err)
			}
			if messages := errs[tc.field]; len(messages) != 1 || !strings.Contains(messages[0], tc.want) {
				t.Errorf("field %q holds %q, want one message saying %q (all: %v)", tc.field, messages, tc.want, errs)
			}
		})
	}

	fault := errors.New("the job store is down")
	if errs, err := launchRefusal(fault); errs != nil || !errors.Is(err, fault) {
		t.Errorf("a fault = %v, %v; want it returned as the error", errs, err)
	}
	if errs, err := launchRefusal(nil); errs != nil || err != nil {
		t.Errorf("an accepted launch = %v, %v; want neither", errs, err)
	}
}
