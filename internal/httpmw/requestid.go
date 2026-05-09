package httpmw

import (
	"net/http"
	"regexp"

	"currency-exchange/internal/idgen"
	"currency-exchange/internal/obs"
)

const (
	// HeaderRequestID is the standard header for request correlation.
	HeaderRequestID = "X-Request-Id"
)

var (
	// validID matches [A-Za-z0-9_-]{1,128} as per docs/discussions/monitoring.md.
	validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

type requestIDConfig struct {
	gen idgen.IDGenerator
}

// Option allows configuring the RequestID middleware.
type Option func(*requestIDConfig)

// WithIDGenerator overrides the default UUID generator.
func WithIDGenerator(gen idgen.IDGenerator) Option {
	return func(c *requestIDConfig) {
		c.gen = gen
	}
}

// RequestID returns a middleware that populates the request context with a
// request ID, either from the X-Request-Id header or newly generated.
func RequestID(next http.Handler, opts ...Option) http.Handler {
	cfg := &requestIDConfig{
		gen: idgen.New(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)

		if id == "" || !validID.MatchString(id) {
			id = cfg.gen.NewID()
		}

		ctx := obs.WithRequestID(r.Context(), id)
		w.Header().Set(HeaderRequestID, id)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
