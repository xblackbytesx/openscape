package handler

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/openscape/openscape/internal/domain"
	"github.com/openscape/openscape/internal/repository"
	"github.com/openscape/openscape/web/templates/pages"
)

type HomeHandler struct {
	galleries *repository.GalleryStore
}

func NewHomeHandler(galleries *repository.GalleryStore) *HomeHandler {
	return &HomeHandler{galleries: galleries}
}

func (h *HomeHandler) Home(c *echo.Context) error {
	ctx := c.Request().Context()
	user := currentUser(c)

	// Logged-in users go to admin dashboard
	if user != nil {
		return c.Redirect(http.StatusFound, "/admin")
	}

	galleries, err := h.galleries.ListPublic(ctx)
	if err != nil {
		galleries = []*domain.Gallery{}
	}

	return pages.Home(galleries, csrfToken(c)).Render(ctx, c.Response())
}
