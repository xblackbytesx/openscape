package middleware

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/openscape/openscape/internal/auth"
	"github.com/openscape/openscape/internal/domain"
	"github.com/openscape/openscape/internal/repository"
)

const (
	CtxUser    = "user"
	CtxCanEdit = "can_edit"
)

// InjectUser loads the current user from session into context (optional — doesn't redirect).
func InjectUser(users *repository.UserStore) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			userID, ok := auth.GetUserID(c.Request())
			if ok {
				u, err := users.GetByID(c.Request().Context(), userID)
				if err == nil && u != nil {
					c.Set(CtxUser, u)
				}
			}
			return next(c)
		}
	}
}

// RequireAuth redirects to /login if no user in context.
func RequireAuth() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if c.Get(CtxUser) == nil {
				if isHTMX(c) {
					c.Response().Header().Set("HX-Redirect", "/login")
					return c.NoContent(http.StatusUnauthorized)
				}
				return c.Redirect(http.StatusFound, "/login")
			}
			return next(c)
		}
	}
}

// RequireAdmin returns 403 if user is not an admin.
func RequireAdmin() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			u, ok := c.Get(CtxUser).(*domain.User)
			if !ok || !u.IsAdmin {
				return echo.ErrForbidden
			}
			return next(c)
		}
	}
}

func isHTMX(c *echo.Context) bool {
	return c.Request().Header.Get("HX-Request") == "true"
}
