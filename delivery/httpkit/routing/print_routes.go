package routing

import (
	"fmt"
	"net/http"
	"strings"

	gochi "github.com/go-chi/chi/v5"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

func PrintRoutes(r gochi.Routes, mode types.ServerMode) {
	type route struct {
		method string
		path   string
	}

	groups := make(map[string][]route)
	var groupOrder []string

	_ = gochi.Walk(r, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		path = strings.TrimSuffix(path, "/*")
		if path == "" {
			path = "/"
		}

		segment := "Root"
		if trimmed := strings.TrimPrefix(path, "/"); trimmed != "" {
			parts := strings.SplitN(trimmed, "/", 2)
			segment = capitalize(parts[0])
		}

		if _, exists := groups[segment]; !exists {
			groupOrder = append(groupOrder, segment)
		}
		groups[segment] = append(groups[segment], route{method: method, path: path})
		return nil
	})

	var maxMethod int
	for _, routes := range groups {
		for _, r := range routes {
			if len(r.method) > maxMethod {
				maxMethod = len(r.method)
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n  Registered Endpoints (%s)\n", mode)
	b.WriteString("  " + strings.Repeat("─", 40) + "\n")

	for i, group := range groupOrder {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "  %s\n", group)
		for _, r := range groups[group] {
			fmt.Fprintf(&b, "    %-*s  %s\n", maxMethod, r.method, r.path)
		}
	}

	b.WriteString("  " + strings.Repeat("─", 40) + "\n")
	fmt.Print(b.String())
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
