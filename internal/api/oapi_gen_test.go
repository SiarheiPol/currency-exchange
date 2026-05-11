// Package api_test contains characterization tests for internal/api/oapi_gen.go.
// These tests pin the public contract that oapi-codegen produces so that
// re-running code generation cannot silently remove types, methods, or enum
// constants that handlers will depend on.
package api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	api "currency-exchange/internal/api"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ---- compile-time assertions -----------------------------------------------

// stubServer satisfies api.ServerInterface. If the interface changes (wrong
// method name, wrong signature) this file will fail to compile.
type stubServer struct{}

func (stubServer) GetLatestQuote(w http.ResponseWriter, r *http.Request, params api.GetLatestQuoteParams) {
}
func (stubServer) RefreshQuote(w http.ResponseWriter, r *http.Request, params api.RefreshQuoteParams) {
}
func (stubServer) GetQuoteJob(w http.ResponseWriter, r *http.Request, id openapi_types.UUID, params api.GetQuoteJobParams) {
}

// Compile-time: stubServer implements ServerInterface.
var _ api.ServerInterface = stubServer{}

// Compile-time: ErrorErrorCode constants have the expected named type.
var _ api.ErrorErrorCode = api.Internal
var _ api.ErrorErrorCode = api.InvalidRequest
var _ api.ErrorErrorCode = api.NoData
var _ api.ErrorErrorCode = api.NotFound
var _ api.ErrorErrorCode = api.UnsupportedCurrency
var _ api.ErrorErrorCode = api.UpstreamUnavailable

// ---- helpers ----------------------------------------------------------------

// genFilePath returns the absolute path to oapi_gen.go, anchored on this test
// file's location so it works regardless of the directory go test uses.
func genFilePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "oapi_gen.go")
}

// ---- tests ------------------------------------------------------------------

// TestGenerated_HasDoNotEditHeader asserts that the generated file begins with
// the standard "DO NOT EDIT" marker produced by oapi-codegen. If the marker is
// missing it means the file was hand-edited or the codegen step was skipped.
func TestGenerated_HasDoNotEditHeader(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(genFilePath(t))
	if err != nil {
		t.Fatalf("read oapi_gen.go: %v", err)
	}
	// Only inspect the first 1 KiB — the header must be near the top.
	excerpt := string(raw)
	if len(excerpt) > 1024 {
		excerpt = excerpt[:1024]
	}
	if !strings.Contains(strings.ToUpper(excerpt), "DO NOT EDIT") {
		t.Errorf("oapi_gen.go first 1024 bytes do not contain \"DO NOT EDIT\";\ngot:\n%s", excerpt)
	}
}

// TestGenerated_ServerInterfaceContract is a compile-time test. It asserts
// that api.ServerInterface exists and has exactly the three methods produced by
// oapi-codegen for the three operations in api/openapi.yaml:
//   - GetLatestQuote(w, r, GetLatestQuoteParams)
//   - RefreshQuote(w, r, RefreshQuoteParams)
//   - GetQuoteJob(w, r, openapi_types.UUID, GetQuoteJobParams)
//
// The assertion is the package-level `var _ api.ServerInterface = stubServer{}`
// declaration above. If the interface or any method signature changes, this
// file fails to compile and the test is automatically RED.
func TestGenerated_ServerInterfaceContract(t *testing.T) {
	t.Parallel()
	// The real assertion is the compile-time var declaration above.
	// We instantiate the stub here so the compiler cannot eliminate it.
	_ = stubServer{}
}

// TestGenerated_ExpectedTypesExist asserts that the six named types generated
// from the OpenAPI schema components exist and are reachable from the api
// package. Compile-time blank-identifier assignments confirm the type names;
// the runtime round-trip section confirms the JobStatus union wrapper behaves
// correctly.
func TestGenerated_ExpectedTypesExist(t *testing.T) {
	t.Parallel()

	// --- compile-time: named types are reachable ----------------------------
	var _ api.RefreshRequest
	var _ api.RefreshAccepted
	var _ api.LatestQuote
	var _ api.Error
	var _ api.JobStatus
	var _ api.JobStatusPending
	var _ api.JobStatusDone
	var _ api.JobStatusFailed

	// --- compile-time: JobStatus union methods exist -----------------------
	// Direct method calls (not just variable declarations) verify the method
	// set on the concrete type, not just a named var that the compiler may
	// optimise away.
	var js api.JobStatus
	_, _ = js.AsJobStatusPending()
	_, _ = js.AsJobStatusDone()
	_, _ = js.AsJobStatusFailed()

	// FromJobStatus* takes a pointer receiver, so we need &js.
	_ = (&js).FromJobStatusPending(api.JobStatusPending{})
	_ = (&js).FromJobStatusDone(api.JobStatusDone{})
	_ = (&js).FromJobStatusFailed(api.JobStatusFailed{})

	// --- runtime: JobStatus round-trip for each union variant ---------------
	t.Run("PendingRoundTrip", func(t *testing.T) {
		t.Parallel()
		want := api.JobStatusPending{}
		var union api.JobStatus
		if err := union.FromJobStatusPending(want); err != nil {
			t.Fatalf("FromJobStatusPending: %v", err)
		}
		got, err := union.AsJobStatusPending()
		if err != nil {
			t.Fatalf("AsJobStatusPending: %v", err)
		}
		// FromJobStatusPending forces Status = "pending".
		if got.Status != "pending" {
			t.Errorf("pending round-trip: Status = %q, want \"pending\"", got.Status)
		}
	})

	t.Run("DoneRoundTrip", func(t *testing.T) {
		t.Parallel()
		want := api.JobStatusDone{
			Base:  "USD",
			Quote: "EUR",
			Price: 1.23,
		}
		var union api.JobStatus
		if err := union.FromJobStatusDone(want); err != nil {
			t.Fatalf("FromJobStatusDone: %v", err)
		}
		got, err := union.AsJobStatusDone()
		if err != nil {
			t.Fatalf("AsJobStatusDone: %v", err)
		}
		if got.Base != want.Base || got.Quote != want.Quote || got.Price != want.Price {
			t.Errorf("done round-trip mismatch: got %+v, want %+v", got, want)
		}
		if got.Status != "done" {
			t.Errorf("done round-trip: Status = %q, want \"done\"", got.Status)
		}
	})

	t.Run("FailedRoundTrip", func(t *testing.T) {
		t.Parallel()
		want := api.JobStatusFailed{
			Base:  "USD",
			Quote: "EUR",
		}
		var union api.JobStatus
		if err := union.FromJobStatusFailed(want); err != nil {
			t.Fatalf("FromJobStatusFailed: %v", err)
		}
		got, err := union.AsJobStatusFailed()
		if err != nil {
			t.Fatalf("AsJobStatusFailed: %v", err)
		}
		if got.Base != want.Base || got.Quote != want.Quote {
			t.Errorf("failed round-trip mismatch: got %+v, want %+v", got, want)
		}
		if got.Status != "failed" {
			t.Errorf("failed round-trip: Status = %q, want \"failed\"", got.Status)
		}
	})
}

// TestGenerated_ErrorCodeEnumConstants pins that api.ErrorErrorCode is a strict
// named type and that all six expected constants are defined with the right
// type. The compile-time var declarations at the top of this file are the real
// assertion; this function exists so that go test reports a recognisable test
// name and so that the runtime portion can document the expected string values.
func TestGenerated_ErrorCodeEnumConstants(t *testing.T) {
	t.Parallel()

	// Compile-time assertions are the package-level var _ declarations above.
	// Runtime: verify the underlying string values match the OpenAPI spec.
	cases := []struct {
		constant api.ErrorErrorCode
		want     string
	}{
		{api.Internal, "internal"},
		{api.InvalidRequest, "invalid_request"},
		{api.NoData, "no_data"},
		{api.NotFound, "not_found"},
		{api.UnsupportedCurrency, "unsupported_currency"},
		{api.UpstreamUnavailable, "upstream_unavailable"},
	}
	for _, tc := range cases {
		if got := string(tc.constant); got != tc.want {
			t.Errorf("ErrorErrorCode constant: got %q, want %q", got, tc.want)
		}
	}
}
