// Package api implements the HTTP handlers for the currency quote service.
// This file contains the Handlers struct satisfying ServerInterface.
// Stage 4 iter 5: stub responses only. Iter 7–9 replace stubs with real
// queue and repository interactions.
package api

import (
	"encoding/json"
	"net/http"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Handlers implements ServerInterface. Zero fields in the skeleton; later
// iterations introduce queue, repo and clock arguments.
type Handlers struct{}

// NewHandlers returns a Handlers ready to serve. Zero dependencies in the
// skeleton; later iterations introduce queue, repo, clock arguments.
func NewHandlers() *Handlers {
	return &Handlers{}
}

// RefreshQuote handles POST /quotes/refresh. Stub: always returns 202 with a
// zero UUID. Iter 7 replaces this with a real queue enqueue.
func (h *Handlers) RefreshQuote(w http.ResponseWriter, r *http.Request, params RefreshQuoteParams) {
	var stubID openapi_types.UUID // all-zero
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", "/quotes/"+stubID.String())
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(RefreshAccepted{Id: stubID})
}

// GetQuoteJob handles GET /quotes/{id}. Stub: always returns 200 with a
// pending JobStatus echoing the path id. Iter 8 replaces this with a real
// DB lookup.
func (h *Handlers) GetQuoteJob(w http.ResponseWriter, r *http.Request, id openapi_types.UUID, params GetQuoteJobParams) {
	pending := JobStatusPending{
		Id:        id,
		Base:      "EUR",
		Quote:     "MXN",
		Status:    Pending,
		CreatedAt: time.Now(),
		Attempts:  0,
	}
	var js JobStatus
	if err := js.FromJobStatusPending(pending); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(js)
}

// GetLatestQuote handles GET /quotes/latest. Stub: always returns 404 with a
// no_data error. Iter 9 replaces this with a real DB lookup.
func (h *Handlers) GetLatestQuote(w http.ResponseWriter, r *http.Request, params GetLatestQuoteParams) {
	var envelope Error
	envelope.Error.Code = NoData
	envelope.Error.Message = "no successful quote yet"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(envelope)
}
