package api

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

type hateoasRecorder struct {
	http.ResponseWriter
	body       *bytes.Buffer
	statusCode int
}

func (rec *hateoasRecorder) Write(b []byte) (int, error) {
	return rec.body.Write(b)
}

func (rec *hateoasRecorder) WriteHeader(statusCode int) {
	rec.statusCode = statusCode
}

// Link represents a HATEOAS link.
type Link struct {
	Rel    string `json:"rel"`
	Href   string `json:"href"`
	Method string `json:"method"`
}

// HATEOASMiddleware intercepts JSON responses and injects _links based on user scopes.
func HATEOASMiddleware(generator auth.HATEOASGenerator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip for non-GET requests or non-API endpoints if needed, but we'll run for all for now.
			
			rec := &hateoasRecorder{
				ResponseWriter: w,
				body:           &bytes.Buffer{},
				statusCode:     http.StatusOK, // default
			}

			next.ServeHTTP(rec, r)

			// If the response is not JSON or is an error, just write it back and return.
			if rec.statusCode >= 400 || rec.Header().Get("Content-Type") != "application/json" {
				w.WriteHeader(rec.statusCode)
				w.Write(rec.body.Bytes())
				return
			}

			// We have a successful JSON response. Inject _links.
			var data map[string]interface{}
			if err := json.Unmarshal(rec.body.Bytes(), &data); err != nil {
				// Failed to unmarshal, just return raw
				w.WriteHeader(rec.statusCode)
				w.Write(rec.body.Bytes())
				return
			}

			// Generate Links
			links := []Link{
				{Rel: "self", Href: r.URL.Path, Method: "GET"},
			}

			if generator != nil {
				allowedMethods, _ := generator.GetAllowedMethods(r.Context(), r.URL.Path)
				for _, m := range allowedMethods {
					if m != "GET" {
						links = append(links, Link{
							Rel:    "action",
							Href:   r.URL.Path,
							Method: m,
						})
					}
				}
			}

			data["_links"] = links

			modifiedJSON, err := json.Marshal(data)
			if err != nil {
				w.WriteHeader(rec.statusCode)
				w.Write(rec.body.Bytes())
				return
			}

			// Write modified response
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(rec.statusCode)
			w.Write(modifiedJSON)
		})
	}
}
