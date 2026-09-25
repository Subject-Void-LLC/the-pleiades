// Tests for recording a method's warnings.
package sdk_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestRecordWarnings: no warnings records nothing, so a run with none
// prints no empty block; warnings are recorded as given; and a context that
// refuses the stat is an error the method returns, never a warning lost.
func TestRecordWarnings(t *testing.T) {
	rc := newRecordingContext()
	if err := sdk.RecordWarnings(rc, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := rc.stats[sdk.StatWarnings]; ok {
		t.Error("no warnings recorded a stat")
	}

	want := []string{"device d1 allows TLS 1.0"}
	if err := sdk.RecordWarnings(rc, want); err != nil {
		t.Fatal(err)
	}
	if got := rc.stats[sdk.StatWarnings]; !reflect.DeepEqual(got, want) {
		t.Errorf("recorded %v, want %v", got, want)
	}

	refusing := newRecordingContext()
	refusing.statErr = errors.New("refused")
	if err := sdk.RecordWarnings(refusing, want); err == nil {
		t.Error("a refused stat was not returned")
	}
}
