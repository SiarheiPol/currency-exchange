package httpmw

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	legacyrouter "github.com/getkin/kin-openapi/routers/legacy"

	"currency-exchange/internal/obs"
)

// OpenAPIValidate returns a middleware that validates HTTP requests and
// responses against the provided OpenAPI 3 spec. Invalid requests receive a
// 400 with code "invalid_request". Invalid responses receive a 500 with code
// "internal"; the validator detail is logged via obs.EvOpenAPIResponseInvalid
// and never sent to the client.
//
// If spec is nil the middleware is a no-op pass-through (compile-time
// signature check only).
func OpenAPIValidate(spec *openapi3.T, next http.Handler) http.Handler {
	if spec == nil {
		return next
	}

	router, err := legacyrouter.NewRouter(spec)
	if err != nil {
		// Spec is malformed — programmer error caught at startup.
		panic("openapi spec router construction failed: " + err.Error())
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ---- Request validation ----
		route, pathParams, err := router.FindRoute(r)
		if err != nil {
			// Path not in spec — pass through; 404 handled by mux.
			next.ServeHTTP(w, r)
			return
		}

		reqInput := &openapi3filter.RequestValidationInput{
			Request:    r,
			PathParams: pathParams,
			Route:      route,
		}
		if err := openapi3filter.ValidateRequest(r.Context(), reqInput); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", shortenValidatorMessage(err.Error()))
			return
		}

		// ---- Capture response for validation ----
		rec := newResponseRecorder(w)
		next.ServeHTTP(rec, r)

		// ---- Response validation ----
		respInput := &openapi3filter.ResponseValidationInput{
			RequestValidationInput: reqInput,
			Status:                 rec.status,
			Header:                 rec.Header(),
		}
		respInput.SetBodyBytes(rec.body.Bytes())

		if err := openapi3filter.ValidateResponse(r.Context(), respInput); err != nil {
			obs.Logger(r.Context()).Error(string(obs.EvOpenAPIResponseInvalid),
				"validator_error", err.Error(),
				"path", r.URL.Path,
				"method", r.Method,
				"handler_status", rec.status,
			)
			writeJSONError(w, http.StatusInternalServerError, "internal", "response failed schema validation")
			return
		}

		// Response is valid — flush captured response to the real writer.
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.status)
		_, _ = w.Write(rec.body.Bytes())
	})
}

// shortenValidatorMessage strips the schema and value dumps that kin-openapi
// appends to validator errors. The validator's full Error() output is useful
// in dev/debug but exposes internal schema fragments to clients. We keep the
// human-readable head ("request body has an error: ..." or "parameter X has
// an error: ...") and drop everything from the first "\nSchema:" onward.
func shortenValidatorMessage(s string) string {
	if i := strings.Index(s, "\nSchema:"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(s, "\r\n\t ")
}

// writeJSONError emits a JSON error envelope. Local copy of the api.writeError
// pattern to avoid an import cycle (api ← httpmw via main).
func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	payload := struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{}
	payload.Error.Code = code
	payload.Error.Message = message
	_ = json.NewEncoder(w).Encode(payload)
}

// responseRecorder buffers status, headers and body from a downstream handler.
type responseRecorder struct {
	real   http.ResponseWriter
	header http.Header
	status int
	body   *bytes.Buffer
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{
		real:   w,
		header: make(http.Header),
		status: http.StatusOK,
		body:   &bytes.Buffer{},
	}
}

func (r *responseRecorder) Header() http.Header         { return r.header }
func (r *responseRecorder) WriteHeader(code int)        { r.status = code }
func (r *responseRecorder) Write(p []byte) (int, error) { return r.body.Write(p) }
