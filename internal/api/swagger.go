package api

import (
	"embed"
	"io/fs"
	"net/http"
)

// swaggerUIFiles holds the vendored Swagger UI distribution.
//
// Vendored from swagger-api/swagger-ui-dist@5.21.0
// (https://github.com/swagger-api/swagger-ui/releases/tag/v5.21.0).
//
// index.html and swagger-initializer.js are PATCHED from upstream to point
// at our /openapi.json spec route. Re-vendoring requires re-applying these
// patches — see the headers of those two files.
//
//go:embed swaggerui
var swaggerUIFiles embed.FS

// swaggerUIRoot is the sub-FS rooted at the embedded swaggerui/ directory,
// so the file server sees index.html at "" (matching the StripPrefix path).
var swaggerUIRoot, _ = fs.Sub(swaggerUIFiles, "swaggerui")

// OpenAPIJSONHandler serves the embedded OpenAPI spec as JSON at /openapi.json.
var OpenAPIJSONHandler http.HandlerFunc = func(w http.ResponseWriter, r *http.Request) {
	body, err := GetSpecJSON()
	if err != nil {
		http.Error(w, "failed to load OpenAPI spec", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// SwaggerUIHandler serves the vendored Swagger UI bundle at /docs/.
var SwaggerUIHandler http.Handler = http.StripPrefix("/docs/", http.FileServer(http.FS(swaggerUIRoot)))
