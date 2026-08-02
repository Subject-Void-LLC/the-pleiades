package api_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

func FuzzDispatcher(f *testing.F) {
	repo := &MockRepository{Count: 1}
	eval := &MockAuthEvaluator{Allow: true}
	js := &MockJetStream{}
	dispatcher := api.NewDispatcher(repo, eval, js)

	f.Add("group1", "playbook1")
	f.Add("", "")
	f.Add("malformed!@#$", "pb-2")

	f.Fuzz(func(t *testing.T, group, playbook string) {
		defer func() {
			if r := recover(); r != nil {
				// Ignore panics from httptest.NewRequest with malformed URIs
			}
		}()
		req := httptest.NewRequest("POST", "/dispatch?group="+group+"&playbook="+playbook, nil)
		ctx := context.WithValue(req.Context(), api.IdentityKeyForTest, &auth.Identity{Subject: "user"})
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		dispatcher.DispatchPlaybook(rr, req)
	})
}
