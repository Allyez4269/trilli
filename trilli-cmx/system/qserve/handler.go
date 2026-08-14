package qserve

import (
	"encoding/json"
	"net/http"

	"trilli-cmx/system/logging"
)

// Handler handles GeoIP lookup requests over HTTP. Auth is the static API key
// passed as ?k=, so this endpoint is registered without the session middleware.
type Handler struct {
	service *Service
}

// NewHandler creates a new GeoIP handler
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// ServeHTTP implements http.Handler. Query params: k (API key), ip (target IP).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logging.Debug(packageName, "Lookup request from %s: %s %s", r.RemoteAddr, r.Method, r.URL.Path)

	// Validate API key.
	apiKey := r.URL.Query().Get("k")
	if !h.service.ValidateAPIKey(apiKey) {
		logging.Error(packageName, "Unauthorized GeoIP request from %s", r.RemoteAddr)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Target IP.
	ipAddr := r.URL.Query().Get("ip")
	if ipAddr == "" {
		http.Error(w, "IP address required", http.StatusBadRequest)
		return
	}

	geoData, err := h.service.LookupIP(ipAddr)
	if err != nil {
		logging.Error(packageName, "IP lookup failed for %s: %v", ipAddr, err)
		http.Error(w, "IP lookup failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	if err := json.NewEncoder(w).Encode(geoData); err != nil {
		logging.Error(packageName, "Failed to encode response for IP %s: %v", ipAddr, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}
