// Package cfapitest is a real HTTPS server answering the part of
// Cloudflare's API v4 that pkg/cfapi reads, as Cloudflare documents it:
// the envelope, page-numbered pagination, and its errors. It holds one
// account's tunnels and connectors, Access applications, organization and
// zones with their records, and refuses any request without its token. It
// is for tests: nothing here reaches Cloudflare.
package cfapitest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Server is the fake API. Its fields may be changed between requests.
type Server struct {
	*httptest.Server
	Token   string
	Account string
	// AccountName is the account's name.
	AccountName string
	Tunnels     []map[string]any
	Connectors  map[string][]map[string]any
	Apps        []map[string]any
	// AppPolicies are policies read from an application's policies
	// endpoint, for an application listing none of its own.
	AppPolicies map[string][]map[string]any
	AuthDomain  string
	Zones       []map[string]any
	Records     map[string][]map[string]any
	// Fail, when set, answers every request to a path with that prefix
	// with this status and a Cloudflare error.
	Fail map[string]int
	// PerPage, when set, overrides the page size a request asks for, to
	// make lists page.
	PerPage  int
	mu       sync.Mutex
	requests []string
}

// New starts a server for account with token.
func New(t *testing.T, account, token string) *Server {
	t.Helper()
	s := &Server{Token: token, Account: account, AccountName: "Example", Connectors: map[string][]map[string]any{},
		AppPolicies: map[string][]map[string]any{}, Records: map[string][]map[string]any{}, Fail: map[string]int{}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Requests is every request, as its path and query.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// fail answers a Cloudflare error.
func fail(w http.ResponseWriter, status, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": code, "message": msg}}, "messages": []any{}, "result": nil})
}

// ok answers result, with result_info when info is set.
func ok(w http.ResponseWriter, result any, info map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	body := map[string]any{"success": true, "errors": []any{}, "messages": []any{}, "result": result}
	if info != nil {
		body["result_info"] = info
	}
	_ = json.NewEncoder(w).Encode(body)
}

// page answers one page of items, as the request's page and per_page ask.
func (s *Server) page(w http.ResponseWriter, r *http.Request, items []map[string]any) {
	per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if s.PerPage > 0 {
		per = s.PerPage
	}
	if per <= 0 {
		per = 20
	}
	pg, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if pg <= 0 {
		pg = 1
	}
	total := (len(items) + per - 1) / per
	if total == 0 {
		total = 1
	}
	start, end := (pg-1)*per, pg*per
	if start > len(items) {
		start = len(items)
	}
	if end > len(items) {
		end = len(items)
	}
	ok(w, items[start:end], map[string]any{"page": pg, "per_page": per, "count": end - start, "total_count": len(items), "total_pages": total})
}

// filter keeps the items whose field equals want, or all when want is "".
func filter(items []map[string]any, field, want string) []map[string]any {
	if want == "" {
		return items
	}
	var out []map[string]any
	for _, it := range items {
		if v, _ := it[field].(string); strings.EqualFold(v, want) {
			out = append(out, it)
		}
	}
	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, r.URL.RequestURI())
	s.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+s.Token {
		fail(w, http.StatusUnauthorized, 10000, "Authentication error")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/client/v4")
	for prefix, status := range s.Fail {
		if strings.HasPrefix(path, prefix) {
			fail(w, status, 10001, "injected failure")
			return
		}
	}
	acct := "/accounts/" + s.Account
	q := r.URL.Query()
	switch {
	case path == acct:
		ok(w, map[string]any{"id": s.Account, "name": s.AccountName, "type": "standard"}, nil)
	case path == acct+"/cfd_tunnel":
		var live []map[string]any
		for _, t := range filter(s.Tunnels, "name", q.Get("name")) {
			if d, _ := t["deleted_at"].(string); d == "" || q.Get("is_deleted") != "false" {
				live = append(live, t)
			}
		}
		s.page(w, r, live)
	case strings.HasPrefix(path, acct+"/cfd_tunnel/") && strings.HasSuffix(path, "/connections"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, acct+"/cfd_tunnel/"), "/connections")
		ok(w, nonNil(s.Connectors[id]), nil)
	case strings.HasPrefix(path, acct+"/cfd_tunnel/"):
		for _, t := range s.Tunnels {
			if t["id"] == strings.TrimPrefix(path, acct+"/cfd_tunnel/") {
				ok(w, t, nil)
				return
			}
		}
		fail(w, http.StatusNotFound, 1003, "Tunnel not found")
	case path == acct+"/access/apps":
		s.page(w, r, s.Apps)
	case strings.HasPrefix(path, acct+"/access/apps/") && strings.HasSuffix(path, "/policies"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, acct+"/access/apps/"), "/policies")
		s.page(w, r, nonNil(s.AppPolicies[id]))
	case strings.HasPrefix(path, acct+"/access/apps/"):
		for _, a := range s.Apps {
			if a["id"] == strings.TrimPrefix(path, acct+"/access/apps/") {
				ok(w, a, nil)
				return
			}
		}
		fail(w, http.StatusNotFound, 12130, "access.api.error.not_found")
	case path == acct+"/access/organizations":
		ok(w, map[string]any{"auth_domain": s.AuthDomain, "name": "Example"}, nil)
	case path == "/zones":
		var mine []map[string]any
		for _, z := range filter(s.Zones, "name", q.Get("name")) {
			if a, _ := z["account"].(map[string]any); q.Get("account.id") == "" || a["id"] == q.Get("account.id") {
				mine = append(mine, z)
			}
		}
		s.page(w, r, mine)
	case strings.HasPrefix(path, "/zones/") && strings.HasSuffix(path, "/dns_records"):
		zone := strings.TrimSuffix(strings.TrimPrefix(path, "/zones/"), "/dns_records")
		s.page(w, r, filter(filter(s.Records[zone], "name", q.Get("name")), "type", q.Get("type")))
	case strings.HasPrefix(path, "/zones/"):
		for _, z := range s.Zones {
			if z["id"] == strings.TrimPrefix(path, "/zones/") {
				ok(w, z, nil)
				return
			}
		}
		fail(w, http.StatusNotFound, 1001, "Invalid zone identifier")
	default:
		fail(w, http.StatusNotFound, 7003, "Could not route to "+path)
	}
}

// nonNil is items, or an empty list for none, as Cloudflare answers.
func nonNil(items []map[string]any) []map[string]any {
	if items == nil {
		return []map[string]any{}
	}
	return items
}
