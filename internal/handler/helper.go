package handler

import (
	"net/http"
	"regexp"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/openscape/openscape/internal/domain"
	"github.com/openscape/openscape/internal/repository"
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

// requireGalleryEditor loads the gallery from :id param and verifies the current
// user is the owner or an editor member. Returns ErrNotFound or ErrForbidden on failure.
func requireGalleryEditor(c *echo.Context, galleries *repository.GalleryStore) (*domain.Gallery, error) {
	ctx := c.Request().Context()
	user := currentUser(c)

	galleryID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return nil, echo.ErrNotFound
	}
	gallery, err := galleries.GetByID(ctx, galleryID)
	if err != nil || gallery == nil {
		return nil, echo.ErrNotFound
	}
	if gallery.OwnerID != user.ID {
		member, err := galleries.GetMember(ctx, galleryID, user.ID)
		if err != nil || member == nil || member.Role != domain.RoleEditor {
			return nil, echo.ErrForbidden
		}
	}
	return gallery, nil
}
