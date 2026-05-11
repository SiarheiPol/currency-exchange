// MODIFIED from upstream swagger-ui-dist@5.21.0: url points at /openapi.json
// instead of the default petstore URL. Re-vendoring requires re-applying
// this change. Note: index.html does NOT load this file (the initializer is
// inlined there); this file is kept for parity and for users who reference
// swagger-initializer.js directly.

window.onload = function() {
  //<editor-fold desc="Changeable Configuration Block">

  // the following lines will be replaced by docker/configurator, when it runs in a docker-container
  window.ui = SwaggerUIBundle({
    url: "/openapi.json",
    dom_id: '#swagger-ui',
    deepLinking: true,
    presets: [
      SwaggerUIBundle.presets.apis,
      SwaggerUIStandalonePreset
    ],
    plugins: [
      SwaggerUIBundle.plugins.DownloadUrl
    ],
    layout: "StandaloneLayout"
  });

  //</editor-fold>
};
