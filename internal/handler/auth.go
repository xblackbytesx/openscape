package handler

import (
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/labstack/echo/v5"
	"github.com/openscape/openscape/internal/auth"
	"github.com/openscape/openscape/internal/domain"
	"github.com/openscape/openscape/internal/repository"
	"github.com/openscape/openscape/web/templates/pages"
)

type AuthHandler struct {
	users             *repository.UserStore
	allowRegistration bool
}

func NewAuthHandler(users *repository.UserStore, allowRegistration bool) *AuthHandler {
	return &AuthHandler{users: users, allowRegistration: allowRegistration}
}

func (h *AuthHandler) LoginGet(c *echo.Context) error {
	if currentUser(c) != nil {
		return c.Redirect(http.StatusFound, "/admin")
	}
	return pages.Login(csrfToken(c), "").Render(c.Request().Context(), c.Response())
}

func (h *AuthHandler) LoginPost(c *echo.Context) error {
	ctx := c.Request().Context()
	email := c.FormValue("email")
	password := c.FormValue("password")

	if email == "" || password == "" {
		return pages.Login(csrfToken(c), "Email and password are required.").Render(ctx, c.Response())
	}

	u, err := h.users.GetByEmail(ctx, email)
	if err != nil || u == nil || !auth.CheckPassword(u.PasswordHash, password) {
		return pages.Login(csrfToken(c), "Invalid email or password.").Render(ctx, c.Response())
	}

	if err := auth.SetUserID(c.Response(), c.Request(), u.ID); err != nil {
		return pages.Login(csrfToken(c), "Login failed. Please try again.").Render(ctx, c.Response())
	}

	next := c.QueryParam("next")
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/admin"
	}
	return redirect(c, next)
}

func (h *AuthHandler) RegisterGet(c *echo.Context) error {
	if !h.allowRegistration {
		return echo.ErrNotFound
	}
	if currentUser(c) != nil {
		return c.Redirect(http.StatusFound, "/admin")
	}
	return pages.Register(csrfToken(c), "").Render(c.Request().Context(), c.Response())
}

func (h *AuthHandler) RegisterPost(c *echo.Context) error {
	if !h.allowRegistration {
		return echo.ErrNotFound
	}
	ctx := c.Request().Context()

	username := c.FormValue("username")
	email := c.FormValue("email")
	password := c.FormValue("password")
	confirm := c.FormValue("confirm_password")

	if username == "" || email == "" || password == "" {
		return pages.Register(csrfToken(c), "All fields are required.").Render(ctx, c.Response())
	}
	if !isValidEmail(email) {
		return pages.Register(csrfToken(c), "Invalid email address.").Render(ctx, c.Response())
	}
	if len(password) < 8 {
		return pages.Register(csrfToken(c), "Password must be at least 8 characters.").Render(ctx, c.Response())
	}
	if password != confirm {
		return pages.Register(csrfToken(c), "Passwords do not match.").Render(ctx, c.Response())
	}

	existing, _ := h.users.GetByEmail(ctx, email)
	if existing != nil {
		return pages.Register(csrfToken(c), "An account with that email already exists.").Render(ctx, c.Response())
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return pages.Register(csrfToken(c), "Server error. Please try again.").Render(ctx, c.Response())
	}

	u := &domain.User{
		Username:     username,
		Email:        email,
		PasswordHash: hash,
		IsAdmin:      false,
	}
	created, err := h.users.Create(ctx, u)
	if err != nil {
		return pages.Register(csrfToken(c), "Could not create account. Username or email may already be taken.").Render(ctx, c.Response())
	}

	if err := auth.SetUserID(c.Response(), c.Request(), created.ID); err != nil {
		return pages.Login(csrfToken(c), "Registered! Please log in.").Render(ctx, c.Response())
	}

	return redirect(c, "/admin")
}

func (h *AuthHandler) Logout(c *echo.Context) error {
	_ = auth.ClearSession(c.Response(), c.Request())
	return c.Redirect(http.StatusFound, "/")
}

// CheckSetup redirects to /setup if no users exist.
// Skips static assets and the setup route itself.
// Caches the "setup complete" state so the DB is queried at most once after startup.
func CheckSetup(users *repository.UserStore) echo.MiddlewareFunc {
	var setupDone atomic.Bool
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			path := c.Request().URL.Path
			if path == "/setup" || strings.HasPrefix(path, "/static/") {
				return next(c)
			}
			if !setupDone.Load() {
				count, err := users.CountAll(c.Request().Context())
				if err == nil && count == 0 {
					return c.Redirect(http.StatusFound, "/setup")
				}
				setupDone.Store(true)
			}
			return next(c)
		}
	}
}
