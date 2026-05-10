package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"currency-exchange/internal/clock"
)

// Server is the HTTP handler for the fake rates provider.
type Server struct {
	state     *State
	accessKey string
	clock     clock.Clock
}

// NewServer returns a new Server backed by state.
func NewServer(state *State, accessKey string, clk clock.Clock) *Server {
	return &Server{state: state, accessKey: accessKey, clock: clk}
}

// ServeHTTP routes requests. Only /live is handled; everything else returns 404.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/live" {
		http.NotFound(w, r)
		return
	}
	s.handleLive(w, r)
}

// successResponse is the JSON envelope returned on a successful /live call.
type successResponse struct {
	Success   bool               `json:"success"`
	Timestamp int64              `json:"timestamp"`
	Source    string             `json:"source"`
	Quotes    map[string]float64 `json:"quotes"`
}

// errorResponse is the JSON envelope returned when the request cannot be served.
type errorResponse struct {
	Success bool      `json:"success"`
	Error   errorBody `json:"error"`
}

// errorBody carries the numeric API error code and a human-readable info string.
type errorBody struct {
	Code int    `json:"code"`
	Info string `json:"info"`
}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// Auth: missing OR empty access_key → 101.
	key := q.Get("access_key")
	if key == "" {
		writeJSON(w, http.StatusOK, errorResponse{
			Success: false,
			Error:   errorBody{Code: 101, Info: "missing or invalid access_key"},
		})
		return
	}
	// Strict mode: if accessKey configured, must match.
	if s.accessKey != "" && key != s.accessKey {
		writeJSON(w, http.StatusOK, errorResponse{
			Success: false,
			Error:   errorBody{Code: 101, Info: "invalid access_key"},
		})
		return
	}

	// Quota check.
	if !s.state.Consume() {
		writeJSON(w, http.StatusOK, errorResponse{
			Success: false,
			Error:   errorBody{Code: 104, Info: "monthly quota exceeded"},
		})
		return
	}

	// Source default: USD.
	source := q.Get("source")
	if source == "" {
		source = "USD"
	}

	// Currencies: comma-separated list. If empty/absent, return all known targets minus source.
	var targets []string
	cs := q.Get("currencies")
	if cs == "" {
		// Use the whitelist — for MVP, hardcode the standard three currencies.
		targets = []string{"USD", "EUR", "MXN"}
	} else {
		targets = strings.Split(cs, ",")
	}

	quotes := s.state.AllRatesForBase(source, targets)

	writeJSON(w, http.StatusOK, successResponse{
		Success:   true,
		Timestamp: s.clock.Now().Unix(),
		Source:    source,
		Quotes:    quotes,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
