package api_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

func BenchmarkDispatcher(b *testing.B) {
	repo := &MockRepository{Count: 100} // Benchmark batch of 100
	eval := &MockAuthEvaluator{Allow: true}
	bus := &mockBus{}
	dispatcher := api.NewDispatcher(repo, eval, bus)

	req := httptest.NewRequest("POST", "/dispatch?group=routers&runbook=pb-1", nil)
	ctx := context.WithValue(req.Context(), api.IdentityKeyForTest, &auth.Identity{Subject: "user"})
	req = req.WithContext(ctx)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		dispatcher.DispatchRunbook(rr, req)
	}
}
