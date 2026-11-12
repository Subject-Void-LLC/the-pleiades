// A lab deleted on a shared controller between the lab list and that lab's
// read, as another user or another run deletes one, through ClientFor.
package cml

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
)

// TestLabs_LeavesOutALabDeletedSinceTheList: a lab deleted after the list
// of labs was read and before that lab is read is left out, and the other
// labs are read as ever. It used to fail the whole call with the deleted
// lab's 404, so on a shared controller any other user deleting a lab at
// that moment failed an import of a different one.
func TestLabs_LeavesOutALabDeletedSinceTheList(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	c := connectFor(t, ctl)
	ctx := context.Background()
	kept, err := c.CreateLab(ctx, "kept")
	if err != nil {
		t.Fatal(err)
	}
	gone, err := c.CreateLab(ctx, "gone")
	if err != nil {
		t.Fatal(err)
	}
	onCall(ctl, func(_ http.ResponseWriter, r *http.Request) bool {
		// The kept lab sorts first, so it is read first: the other is
		// deleted as it is, after the list named both.
		if r.URL.Path == "/api/v0/labs/"+kept.ID {
			delete(ctl.Labs, gone.ID)
		}
		return false
	})
	labs, err := c.Labs(ctx)
	if err != nil {
		t.Fatalf("Labs with a lab deleted since the list = %v", err)
	}
	if len(labs) != 1 || labs[0].ID != kept.ID || labs[0].Title != "kept" {
		t.Errorf("Labs = %+v, want only the kept lab", labs)
	}
	// Any other failure still fails the call.
	onCall(ctl, nil)
	ctl.Mu.Lock()
	ctl.Refuse = map[string]int{"GET /labs/" + kept.ID: http.StatusForbidden}
	ctl.Mu.Unlock()
	var se *StatusError
	if _, err := c.Labs(ctx); !errors.As(err, &se) || se.Status != http.StatusForbidden {
		t.Errorf("Labs with a lab refused = %v, want the 403", err)
	}
}
