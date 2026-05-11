// Package api implements the HTTP handlers for the currency quote service.
// This file contains the Handlers struct satisfying ServerInterface.
// Stage 4 iter 5: stub responses only. Iter 7–9 replace stubs with real
// queue and repository interactions.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
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
	"currency-exchange/internal/quoterepo"
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
	repo             quoterepo.QuoteRepo
}

// NewHandlers returns a Handlers ready to serve.
func NewHandlers(whitelist []string, q queue.JobQueue, clk clock.Clock, ig idgen.IDGenerator, window time.Duration, repo quoterepo.QuoteRepo) *Handlers {
	return &Handlers{
		whitelist:        whitelist,
		q:                q,
		clk:              clk,
		idgen:            ig,
		coalescingWindow: window,
		repo:             repo,
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

// GetQuoteJob handles GET /quotes/{id}. Returns the current status of the job,
// with appropriate Cache-Control, ETag, and conditional 304 support.
func (h *Handlers) GetQuoteJob(w http.ResponseWriter, r *http.Request, id openapi_types.UUID, _ GetQuoteJobParams) {
	view, err := h.q.GetByID(r.Context(), queue.JobID(uuid.UUID(id).String()))
	if errors.Is(err, queue.ErrNotFound) {
		writeError(w, http.StatusNotFound, newError(NotFound, "job not found"))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, newError(Internal, "could not load job"))
		return
	}
	// API status mapping: internal "running" surfaces as "pending" to clients.
	apiStatus := view.Status
	if apiStatus == "running" {
		apiStatus = "pending"
	}
	switch apiStatus {
	case "pending":
		renderPending(w, view)
	case "done":
		renderDone(w, r, view)
	case "failed":
		renderFailed(w, r, view)
	default:
		writeError(w, http.StatusInternalServerError, newError(Internal, "unknown job status"))
	}
}

// renderPending writes a 200 pending response with no-store cache headers.
func renderPending(w http.ResponseWriter, view queue.JobView) {
	parsedUUID, _ := uuid.Parse(string(view.ID))
	body := JobStatusPending{
		Id:     openapi_types.UUID(parsedUUID),
		Status: Pending,
	}
	var js JobStatus
	if err := js.FromJobStatusPending(body); err != nil {
		writeError(w, http.StatusInternalServerError, newError(Internal, "could not build response"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(js)
}

// renderDone writes a 200 done response (or 304 if ETag matches).
func renderDone(w http.ResponseWriter, r *http.Request, view queue.JobView) {
	etag := fmt.Sprintf(`"%s-done"`, string(view.ID))
	w.Header().Set("Cache-Control", "private, max-age=3600, immutable")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	parsedUUID, _ := uuid.Parse(string(view.ID))
	priceF, _ := view.Price.Float64()
	body := JobStatusDone{
		Id:        openapi_types.UUID(parsedUUID),
		Base:      view.Base,
		Quote:     view.Quote,
		Status:    Done,
		Price:     priceF,
		UpdatedAt: *view.QuoteUpdatedAt,
	}
	var js JobStatus
	if err := js.FromJobStatusDone(body); err != nil {
		writeError(w, http.StatusInternalServerError, newError(Internal, "could not build response"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(js)
}

// renderFailed writes a 200 failed response (or 304 if ETag matches).
func renderFailed(w http.ResponseWriter, r *http.Request, view queue.JobView) {
	etag := fmt.Sprintf(`"%s-failed"`, string(view.ID))
	w.Header().Set("Cache-Control", "private, max-age=3600, immutable")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	parsedUUID, _ := uuid.Parse(string(view.ID))
	body := JobStatusFailed{
		Id:          openapi_types.UUID(parsedUUID),
		Base:        view.Base,
		Quote:       view.Quote,
		Status:      Failed,
		CompletedAt: *view.CompletedAt,
		Error:       view.LastError,
	}
	var js JobStatus
	if err := js.FromJobStatusFailed(body); err != nil {
		writeError(w, http.StatusInternalServerError, newError(Internal, "could not build response"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(js)
}

// GetLatestQuote handles GET /quotes/latest. Returns the most recent successful
// quote for the given pair with Cache-Control, ETag, and conditional 304 support.
func (h *Handlers) GetLatestQuote(w http.ResponseWriter, r *http.Request, params GetLatestQuoteParams) {
	if status, e := h.validatePair(params.Base, params.Quote); e != nil {
		writeError(w, status, e)
		return
	}
	quote, err := h.repo.GetLatest(r.Context(), params.Base, params.Quote)
	if errors.Is(err, quoterepo.ErrNoData) {
		writeError(w, http.StatusNotFound, newError(NoData, "no successful quote yet"))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, newError(Internal, "could not load latest quote"))
		return
	}
	etag := fmt.Sprintf(`"%s-%s-%d"`, params.Base, params.Quote, quote.FetchedAt.Unix())
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(h.coalescingWindow.Seconds())))
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	priceF, _ := quote.Price.Float64()
	body := LatestQuote{
		Base:      params.Base,
		Quote:     params.Quote,
		Price:     priceF,
		UpdatedAt: quote.FetchedAt,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}
