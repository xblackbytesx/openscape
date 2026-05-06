package middleware

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/openscape/openscape/internal/auth"
	"github.com/openscape/openscape/internal/domain"
	"github.com/openscape/openscape/internal/httpx"
	"github.com/openscape/openscape/internal/repository"
)

const (
	CtxGallery = "gallery"
)

// CheckGalleryAccess loads the gallery referenced by :slug and applies the
// shared visibility rules from auth.CheckGalleryAccess. On failure it issues
// the appropriate redirect (to /unlock for protected galleries, to /login
// for private galleries with no user) so the gallery page UX stays smooth.
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

			cookieValue := ""
			if cookie, err := c.Cookie(domain.GalSessionCookiePrefix + slug); err == nil {
				cookieValue = cookie.Value
			}

			result := auth.CheckGalleryAccess(ctx, gallery, user, cookieValue,
				func(ctx context.Context, token string) bool {
					gs, err := galSessions.GetByGallery(ctx, token, gallery.ID)
					return err == nil && gs != nil
				},
				func(ctx context.Context) *domain.GalleryMember {
					if user == nil {
						return nil
					}
					member, _ := galleries.GetMember(ctx, gallery.ID, user.ID)
					return member
				},
			)

			switch {
			case result.RequiresUnlock:
				return c.Redirect(http.StatusFound, "/g/"+slug+"/unlock")
			case result.RequiresLogin:
				if httpx.IsHTMX(c) {
					c.Response().Header().Set("HX-Redirect", "/login")
					return c.NoContent(http.StatusUnauthorized)
				}
				return c.Redirect(http.StatusFound, "/login")
			case result.Forbidden:
				return echo.ErrForbidden
			case !result.Allowed:
				return echo.ErrForbidden
			}

			c.Set(CtxGallery, gallery)
			if result.CanEdit {
				c.Set(CtxCanEdit, true)
			}
			return next(c)
		}
	}
}
