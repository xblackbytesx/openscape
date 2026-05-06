// Package httpx contains tiny HTTP helpers shared by handlers and middleware.
package httpx

import "github.com/labstack/echo/v5"

// IsHTMX returns true when the request originated from an HTMX boost/swap
// (rather than a full-page navigation). Used to decide whether to issue an
// HX-Redirect header or a 30x redirect.
func IsHTMX(c *echo.Context) bool {
	return c.Request().Header.Get("HX-Request") == "true"
}
