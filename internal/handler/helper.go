package handler

import (
	"net/http"
	"regexp"

	"github.com/labstack/echo/v5"
	"github.com/openscape/openscape/internal/domain"
)

var emailRe = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
var slugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func isHTMX(c *echo.Context) bool {
	return c.Request().Header.Get("HX-Request") == "true"
}

func csrfToken(c *echo.Context) string {
	v, _ := c.Get("csrf").(string)
	return v
}

func currentUser(c *echo.Context) *domain.User {
	u, _ := c.Get("user").(*domain.User)
	return u
}

func canEdit(c *echo.Context) bool {
	v, _ := c.Get("can_edit").(bool)
	return v
}

func currentGallery(c *echo.Context) *domain.Gallery {
	g, _ := c.Get("gallery").(*domain.Gallery)
	return g
}

func isValidEmail(email string) bool {
	return emailRe.MatchString(email)
}

func isValidSlug(slug string) bool {
	return len(slug) >= 1 && len(slug) <= 100 && slugRe.MatchString(slug)
}

func redirect(c *echo.Context, path string) error {
	if isHTMX(c) {
		c.Response().Header().Set("HX-Redirect", path)
		return c.NoContent(http.StatusOK)
	}
	return c.Redirect(http.StatusFound, path)
}
