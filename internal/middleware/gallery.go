package middleware

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/openscape/openscape/internal/domain"
	"github.com/openscape/openscape/internal/repository"
)

const (
	CtxGallery     = "gallery"
	galSessionPrefix = "openscape_gs_"
)

// CheckGalleryAccess loads the gallery and checks visibility rules.
// Must be placed after InjectUser.
func CheckGalleryAccess(galleries *repository.GalleryStore, galSessions *repository.GallerySessionStore) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			slug := c.Param("slug")
			ctx := c.Request().Context()

			gallery, err := galleries.GetBySlug(ctx, slug)
			if err != nil || gallery == nil {
				return echo.ErrNotFound
			}

			user, _ := c.Get(CtxUser).(*domain.User)

			// Owner always has access
			if user != nil && gallery.OwnerID == user.ID {
				c.Set(CtxGallery, gallery)
				c.Set(CtxCanEdit, true)
				return next(c)
			}

			// Admin always has access
			if user != nil && user.IsAdmin {
				c.Set(CtxGallery, gallery)
				c.Set(CtxCanEdit, false)
				return next(c)
			}

			switch gallery.Visibility {
			case domain.VisibilityPublic, domain.VisibilityUnlisted:
				c.Set(CtxGallery, gallery)

			case domain.VisibilityUnlistedProtected:
				cookieName := fmt.Sprintf("%s%s", galSessionPrefix, slug)
				cookie, err := c.Cookie(cookieName)
				if err != nil || cookie.Value == "" {
					return c.Redirect(http.StatusFound, "/g/"+slug+"/unlock")
				}
				gs, err := galSessions.GetByGallery(ctx, cookie.Value, gallery.ID)
				if err != nil || gs == nil {
					return c.Redirect(http.StatusFound, "/g/"+slug+"/unlock")
				}
				c.Set(CtxGallery, gallery)

			case domain.VisibilityPrivate:
				if user == nil {
					if isHTMX(c) {
						c.Response().Header().Set("HX-Redirect", "/login")
						return c.NoContent(http.StatusUnauthorized)
					}
					return c.Redirect(http.StatusFound, "/login")
				}
				// Check gallery_members
				member, err := galleries.GetMember(ctx, gallery.ID, user.ID)
				if err != nil || member == nil {
					return echo.ErrForbidden
				}
				c.Set(CtxGallery, gallery)
				if member.Role == domain.RoleEditor {
					c.Set(CtxCanEdit, true)
				}
			}

			return next(c)
		}
	}
}
