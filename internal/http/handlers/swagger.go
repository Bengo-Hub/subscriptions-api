package handlers

import (
	"net/http"

	"github.com/swaggo/swag"
)

// OpenAPIJSON serves the OpenAPI/Swagger JSON specification.
func OpenAPIJSON(w http.ResponseWriter, r *http.Request) {
	spec, _ := swag.ReadDoc("swagger")
	swaggerSpec := []byte(spec)
	if len(swaggerSpec) == 0 {
		swaggerSpec = []byte("{}")
	}
	if r.Method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(swaggerSpec)
}

// SwaggerUI serves the Swagger UI HTML page.
func SwaggerUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
  <head>
    <meta charset="UTF-8">
    <title>Subscriptions API Docs</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
    <style>
      html { box-sizing: border-box; overflow: -moz-scrollbars-vertical; overflow-y: scroll; }
      *, *:before, *:after { box-sizing: inherit; }
      body { margin: 0; background: #fafafa; }
    </style>
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js" charset="UTF-8"></script>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-standalone-preset.js" charset="UTF-8"></script>
    <script>
      window.onload = () => {
        window.ui = SwaggerUIBundle({
          url: window.location.protocol + '//' + window.location.host + '/api/v1/openapi.json',
          dom_id: '#swagger-ui',
          presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
          plugins: [SwaggerUIBundle.plugins.DownloadUrl],
          layout: "StandaloneLayout",
          deepLinking: true,
          filter: true,
          persistAuthorization: true,
        })
      }
    </script>
  </body>
</html>`))
}
