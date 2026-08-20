package swagger

import (
	"encoding/json"
	"net/http"

	gochi "github.com/go-chi/chi/v5"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

func Mount(mux *gochi.Mux, routes []routing.RouteMeta, mode types.ServerMode, cfg Config) {
	spec := Generate(routes, mode, cfg)

	title := cfg.Title
	if title == "" {
		title = "API"
	}
	pageTitle := title + " (" + string(mode) + ") — Documentation"

	mux.Get("/swagger/spec.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(spec)
	})

	mux.Get("/swagger", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(swaggerUIHTML(pageTitle)))
	})
}

func swaggerUIHTML(title string) string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>` + title + `</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  <style>
    body { margin: 0; }
    .topbar { display: none; }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({
      url: '/swagger/spec.json',
      dom_id: '#swagger-ui',
      presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
      layout: 'BaseLayout',
    });
  </script>
</body>
</html>`
}
