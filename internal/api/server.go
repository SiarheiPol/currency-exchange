// Package api implements the HTTP handlers for the currency quote service.
// This file contains the Handlers struct satisfying ServerInterface.
// Stage 4 iter 5: stub responses only. Iter 7–9 replace stubs with real
// queue and repository interactions.
package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/idgen"
	"currency-exchange/internal/queue"
)

// pairFormatRE matches exactly three uppercase ASCII letters.
var pairFormatRE = regexp.MustCompile(`^[A-Z]{3}$`)

// Handlers implements ServerInterface.
type Handlers struct {
	whitelist        []string
	q                queue.JobQueue
	clk              clock.Clock
	idgen            idgen.IDGenerator
	coalescingWindow time.Duration
}

// NewHandlers returns a Handlers ready to serve.
func NewHandlers(whitelist []string, q queue.JobQueue, clk clock.Clock, ig idgen.IDGenerator, window time.Duration) *Handlers {
	return &Handlers{
		whitelist:        whitelist,
		q:                q,
		clk:              clk,
		idgen:            ig,
		coalescingWindow: window,
	}
}

// validatePair runs the three-step validation check:
//  1. Format: both base and quote must match ^[A-Z]{3}$.
//  2. Whitelist: both must be in h.whitelist.
//  3. Self-pair: base and quote must differ.
//
// Returns (http status, *Error) on failure; (0, nil) on success.
func (h *Handlers) validatePair(base, quote string) (int, *Error) {
	if !pairFormatRE.MatchString(base) || !pairFormatRE.MatchString(quote) {
		return http.StatusBadRequest, newError(InvalidRequest, "base and quote must be 3 uppercase letters")
	}
	if !slices.Contains(h.whitelist, base) || !slices.Contains(h.whitelist, quote) {
		return http.StatusBadRequest, newError(UnsupportedCurrency, "currency not supported")
	}
	if base == quote {
		return http.StatusBadRequest, newError(InvalidRequest, "base and quote must differ")
	}
	return 0, nil
}

// newError constructs an api.Error envelope.
func newError(code ErrorErrorCode, msg string) *Error {
	e := &Error{}
	e.Error.Code = code
	e.Error.Message = msg
	return e
}

// writeError serializes an api.Error to the response with the given status,
// Content-Type: application/json, and Cache-Control: no-store.
func writeError(w http.ResponseWriter, status int, e *Error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(e)
}

// JSONErrorHandler is the ErrorHandlerFunc plumbed into api.StdHTTPServerOptions
// so that codegen-wrapper parse failures (missing required query param,
// malformed UUID path param, etc.) emit a consistent JSON error envelope.
func JSONErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	writeError(w, http.StatusBadRequest, newError(InvalidRequest, err.Error()))
}

// RefreshQuote handles POST /quotes/refresh. Generates a UUID, enqueues a
// refresh job, and returns 202 with the queue-returned id (coalesced duplicates
// surface the original id).
func (h *Handlers) RefreshQuote(w http.ResponseWriter, r *http.Request, params RefreshQuoteParams) {
	if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusBadRequest, newError(InvalidRequest, "Content-Type must be application/json"))
		return
	}
	var req RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, newError(InvalidRequest, "invalid JSON body"))
		return
	}
	if status, e := h.validatePair(req.Base, req.Quote); e != nil {
		writeError(w, status, e)
		return
	}
	now := h.clk.Now()
	job := queue.Job{
		ID:        queue.JobID(h.idgen.NewID()),
		Base:      req.Base,
		Quote:     req.Quote,
		DedupKey:  queue.DedupKey(req.Base, req.Quote, now, h.coalescingWindow),
		Source:    "refresh",
		NextRunAt: now,
	}
	returnedID, _, err := h.q.Enqueue(r.Context(), job)
	if err != nil {
		writeError(w, http.StatusInternalServerError, newError(Internal, "could not enqueue refresh job"))
		return
	}
	parsedUUID, err := uuid.Parse(string(returnedID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, newError(Internal, "could not enqueue refresh job"))
		return
	}
	idStr := parsedUUID.String()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", "/quotes/"+idStr)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(RefreshAccepted{Id: openapi_types.UUID(parsedUUID)})
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
// no_data error for valid pairs. Iter 9 replaces this with a real DB lookup.
func (h *Handlers) GetLatestQuote(w http.ResponseWriter, r *http.Request, params GetLatestQuoteParams) {
	if status, e := h.validatePair(params.Base, params.Quote); e != nil {
		writeError(w, status, e)
		return
	}
	var envelope Error
	envelope.Error.Code = NoData
	envelope.Error.Message = "no successful quote yet"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(envelope)
}
