package handler

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/openscape/openscape/internal/auth"
	"github.com/openscape/openscape/internal/domain"
	"github.com/openscape/openscape/internal/repository"
	"github.com/openscape/openscape/web/templates/pages"
)

type SetupHandler struct {
	users *repository.UserStore
}

func NewSetupHandler(users *repository.UserStore) *SetupHandler {
	return &SetupHandler{users: users}
}

func (h *SetupHandler) Get(c *echo.Context) error {
	count, err := h.users.CountAll(c.Request().Context())
	if err != nil || count > 0 {
		return c.Redirect(http.StatusFound, "/")
	}
	return pages.Setup(csrfToken(c), "").Render(c.Request().Context(), c.Response())
}

func (h *SetupHandler) Post(c *echo.Context) error {
	ctx := c.Request().Context()
	count, err := h.users.CountAll(ctx)
	if err != nil || count > 0 {
		return c.Redirect(http.StatusFound, "/")
	}

	username := c.FormValue("username")
	email := c.FormValue("email")
	password := c.FormValue("password")
	confirm := c.FormValue("confirm_password")

	if username == "" || email == "" || password == "" {
		return pages.Setup(csrfToken(c), "All fields are required.").Render(ctx, c.Response())
	}
	if !isValidEmail(email) {
		return pages.Setup(csrfToken(c), "Invalid email address.").Render(ctx, c.Response())
	}
	if len(password) < 8 {
		return pages.Setup(csrfToken(c), "Password must be at least 8 characters.").Render(ctx, c.Response())
	}
	if password != confirm {
		return pages.Setup(csrfToken(c), "Passwords do not match.").Render(ctx, c.Response())
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return pages.Setup(csrfToken(c), "Server error. Please try again.").Render(ctx, c.Response())
	}

	u := &domain.User{
		Username:     username,
		Email:        email,
		PasswordHash: hash,
		IsAdmin:      true,
	}
	created, err := h.users.Create(ctx, u)
	if err != nil {
		return pages.Setup(csrfToken(c), "Could not create account. Username or email may already be taken.").Render(ctx, c.Response())
	}

	if err := auth.SetUserID(c.Response(), c.Request(), created.ID); err != nil {
		return pages.Setup(csrfToken(c), "Setup complete but session error. Please log in.").Render(ctx, c.Response())
	}

	return redirect(c, "/admin")
}
