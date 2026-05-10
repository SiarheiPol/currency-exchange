// Package api_test contains structural tests for api/openapi.yaml.
// These tests use kin-openapi to load and validate the spec, asserting the
// structural invariants defined in docs/discussions/api-contract.md and the
// pinned decisions from the spec-author contract.
package api_test

import (
	"context"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// specPath returns the absolute path to api/openapi.yaml, anchored to the
// location of this test file so it works regardless of the working directory
// go test uses.
func specPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// thisFile is .../internal/api/openapi_spec_test.go
	// api/openapi.yaml is two directories up, then into api/
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "api", "openapi.yaml")
}

// loadSpec loads the OpenAPI spec without performing validation. It is a
// shared helper used by tests that only need to inspect structure.
func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(specPath(t))
	if err != nil {
		t.Fatalf("load api/openapi.yaml: %v", err)
	}
	return doc
}

// operationByID walks doc and returns the Operation whose OperationID matches
// id, together with the HTTP status-code string. Returns nil if not found.
func operationByID(doc *openapi3.T, id string) *openapi3.Operation {
	for _, pathItem := range doc.Paths.Map() {
		if pathItem == nil {
			continue
		}
		ops := pathItem.Operations()
		for _, op := range ops {
			if op != nil && op.OperationID == id {
				return op
			}
		}
	}
	return nil
}

// TestOpenAPISpec_LoadsAndValidates asserts that api/openapi.yaml exists, can
// be loaded by kin-openapi, and passes the library's built-in structural
// validation. A missing file or a structurally invalid spec both fail here.
func TestOpenAPISpec_LoadsAndValidates(t *testing.T) {
	t.Parallel()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(specPath(t))
	if err != nil {
		t.Fatalf("load api/openapi.yaml: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("openapi3 validation failed: %v", err)
	}
}

// TestOpenAPISpec_VersionAndPaths asserts that the spec declares version
// "0.1.0" and contains exactly the three paths defined in api-contract.md.
func TestOpenAPISpec_VersionAndPaths(t *testing.T) {
	t.Parallel()
	doc := loadSpec(t)

	if got := doc.Info.Version; got != "0.1.0" {
		t.Errorf("info.version: got %q, want %q", got, "0.1.0")
	}

	wantPaths := []string{
		"/quotes/refresh",
		"/quotes/{id}",
		"/quotes/latest",
	}
	pathsMap := doc.Paths.Map()
	if got := len(pathsMap); got != len(wantPaths) {
		t.Errorf("paths count: got %d, want %d", got, len(wantPaths))
	}
	for _, p := range wantPaths {
		if _, ok := pathsMap[p]; !ok {
			t.Errorf("missing path %q in spec", p)
		}
	}
}

// TestOpenAPISpec_OperationIds asserts that the three operations use the
// operationId values pinned in the spec-author contract.
func TestOpenAPISpec_OperationIds(t *testing.T) {
	t.Parallel()
	doc := loadSpec(t)

	cases := []struct {
		path     string
		method   string // "Post" or "Get"
		wantOpID string
	}{
		{"/quotes/refresh", "Post", "refreshQuote"},
		{"/quotes/{id}", "Get", "getQuoteJob"},
		{"/quotes/latest", "Get", "getLatestQuote"},
	}

	for _, tc := range cases {
		pathItem := doc.Paths.Value(tc.path)
		if pathItem == nil {
			t.Errorf("path %q not found in spec", tc.path)
			continue
		}
		var op *openapi3.Operation
		switch tc.method {
		case "Post":
			op = pathItem.Post
		case "Get":
			op = pathItem.Get
		}
		if op == nil {
			t.Errorf("path %q has no %s operation", tc.path, tc.method)
			continue
		}
		if op.OperationID != tc.wantOpID {
			t.Errorf("path %q %s operationId: got %q, want %q", tc.path, tc.method, op.OperationID, tc.wantOpID)
		}
	}
}

// TestOpenAPISpec_StatusCodesComplete asserts that each operation declares all
// expected HTTP status codes as defined in api-contract.md.
func TestOpenAPISpec_StatusCodesComplete(t *testing.T) {
	t.Parallel()
	doc := loadSpec(t)

	cases := []struct {
		operationID string
		wantCodes   []string
	}{
		{"refreshQuote", []string{"202", "400", "500"}},
		{"getQuoteJob", []string{"200", "304", "400", "404", "500"}},
		{"getLatestQuote", []string{"200", "304", "400", "404", "500"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.operationID, func(t *testing.T) {
			t.Parallel()
			op := operationByID(doc, tc.operationID)
			if op == nil {
				t.Fatalf("operation %q not found in spec", tc.operationID)
			}
			if op.Responses == nil {
				t.Fatalf("operation %q has no responses", tc.operationID)
			}
			respMap := op.Responses.Map()
			for _, code := range tc.wantCodes {
				if _, ok := respMap[code]; !ok {
					t.Errorf("operation %q: missing response code %q", tc.operationID, code)
				}
			}
		})
	}
}

// TestOpenAPISpec_ErrorCodeEnum asserts that the Error schema's code field
// enum contains all six error codes defined in api-contract.md. Additional
// codes are allowed (so future additions do not break this test).
func TestOpenAPISpec_ErrorCodeEnum(t *testing.T) {
	t.Parallel()
	doc := loadSpec(t)

	errorSchemaRef, ok := doc.Components.Schemas["Error"]
	if !ok {
		t.Fatal("components.schemas.Error not found in spec")
	}
	if errorSchemaRef.Value == nil {
		t.Fatal("components.schemas.Error has nil Value")
	}
	errorSchema := errorSchemaRef.Value

	// The Error schema must have a property named "error" containing "code".
	errorProp, ok := errorSchema.Properties["error"]
	if !ok {
		t.Fatal("Error schema missing property \"error\"")
	}
	if errorProp.Value == nil {
		t.Fatal("Error schema property \"error\" has nil Value")
	}
	codeProp, ok := errorProp.Value.Properties["code"]
	if !ok {
		t.Fatal("Error.error schema missing property \"code\"")
	}
	if codeProp.Value == nil {
		t.Fatal("Error.error.code schema has nil Value")
	}

	rawEnum := codeProp.Value.Enum
	enum := make([]string, 0, len(rawEnum))
	for _, v := range rawEnum {
		if s, ok := v.(string); ok {
			enum = append(enum, s)
		}
	}

	wants := []string{
		"invalid_request",
		"unsupported_currency",
		"no_data",
		"not_found",
		"upstream_unavailable",
		"internal",
	}
	for _, want := range wants {
		if !slices.Contains(enum, want) {
			t.Errorf("Error.error.code enum missing %q; got %v", want, enum)
		}
	}
}

// TestOpenAPISpec_RequiredHeaders asserts that named responses declare the
// required response headers per api-contract.md.
func TestOpenAPISpec_RequiredHeaders(t *testing.T) {
	t.Parallel()
	doc := loadSpec(t)

	cases := []struct {
		operationID string
		statusCode  string
		wantHeaders []string
	}{
		{"refreshQuote", "202", []string{"Location", "Cache-Control"}},
		{"getQuoteJob", "200", []string{"Cache-Control", "ETag", "Retry-After"}},
		{"getQuoteJob", "304", []string{"ETag"}},
		{"getLatestQuote", "200", []string{"Cache-Control", "ETag"}},
		{"getLatestQuote", "304", []string{"ETag"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.operationID+"/"+tc.statusCode, func(t *testing.T) {
			t.Parallel()
			op := operationByID(doc, tc.operationID)
			if op == nil {
				t.Fatalf("operation %q not found in spec", tc.operationID)
			}
			if op.Responses == nil {
				t.Fatalf("operation %q has no responses", tc.operationID)
			}
			respRef := op.Responses.Value(tc.statusCode)
			if respRef == nil {
				t.Fatalf("operation %q has no response for status %q", tc.operationID, tc.statusCode)
			}
			if respRef.Value == nil {
				t.Fatalf("operation %q response %q has nil Value", tc.operationID, tc.statusCode)
			}
			headers := respRef.Value.Headers
			for _, h := range tc.wantHeaders {
				if _, ok := headers[h]; !ok {
					t.Errorf("operation %q, status %q: missing header %q", tc.operationID, tc.statusCode, h)
				}
			}
		})
	}
}

// TestOpenAPISpec_QueryParamsLatest asserts that the getLatestQuote operation
// has exactly two required query parameters — "base" and "quote" — each with
// the pattern constraint "^[A-Z]{3}$".
func TestOpenAPISpec_QueryParamsLatest(t *testing.T) {
	t.Parallel()
	doc := loadSpec(t)

	op := operationByID(doc, "getLatestQuote")
	if op == nil {
		t.Fatal("operation getLatestQuote not found in spec")
	}

	var queryParams []*openapi3.Parameter
	for _, paramRef := range op.Parameters {
		if paramRef == nil || paramRef.Value == nil {
			continue
		}
		if paramRef.Value.In == "query" {
			queryParams = append(queryParams, paramRef.Value)
		}
	}

	if got := len(queryParams); got != 2 {
		t.Fatalf("getLatestQuote query param count: got %d, want 2", got)
	}

	wantNames := []string{"base", "quote"}
	for _, p := range queryParams {
		if !slices.Contains(wantNames, p.Name) {
			t.Errorf("unexpected query param %q; expected one of %v", p.Name, wantNames)
		}
		if !p.Required {
			t.Errorf("query param %q: Required = false, want true", p.Name)
		}
		if p.Schema == nil || p.Schema.Value == nil {
			t.Errorf("query param %q: Schema is nil", p.Name)
			continue
		}
		const wantPattern = `^[A-Z]{3}$`
		if got := p.Schema.Value.Pattern; got != wantPattern {
			t.Errorf("query param %q: Pattern = %q, want %q", p.Name, got, wantPattern)
		}
	}

	// Assert both expected names are present.
	for _, name := range wantNames {
		found := false
		for _, p := range queryParams {
			if p.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("getLatestQuote: missing required query param %q", name)
		}
	}
}
