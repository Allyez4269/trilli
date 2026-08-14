package apikeys

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.json
var openAPISpec []byte

// OpenAPI serves the machine-readable OpenAPI 3 description of the /api/v1
// surface. It's public (the contract carries no secrets) so it can be imported
// into Postman/Insomnia or used to generate client SDKs.
func (h *Handlers) OpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(openAPISpec)
}
