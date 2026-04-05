package handler

import (
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/openscape/openscape/internal/auth"
	"github.com/openscape/openscape/internal/domain"
	"github.com/openscape/openscape/internal/repository"
	"github.com/openscape/openscape/web/templates/pages"
)

type UsersHandler struct {
	users *repository.UserStore
}

func NewUsersHandler(users *repository.UserStore) *UsersHandler {
	return &UsersHandler{users: users}
}

func (h *UsersHandler) List(c *echo.Context) error {
	ctx := c.Request().Context()
	users, err := h.users.List(ctx)
	if err != nil {
		users = []*domain.User{}
	}
	return pages.AdminUsers(currentUser(c), users, csrfToken(c), "").Render(ctx, c.Response())
}

func (h *UsersHandler) Create(c *echo.Context) error {
	ctx := c.Request().Context()
	admin := currentUser(c)

	username := c.FormValue("username")
	email := c.FormValue("email")
	password := c.FormValue("password")

	if username == "" || email == "" || password == "" {
		users, _ := h.users.List(ctx)
		return pages.AdminUsers(admin, users, csrfToken(c), "All fields are required.").Render(ctx, c.Response())
	}
	if !isValidEmail(email) {
		users, _ := h.users.List(ctx)
		return pages.AdminUsers(admin, users, csrfToken(c), "Invalid email address.").Render(ctx, c.Response())
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		users, _ := h.users.List(ctx)
		return pages.AdminUsers(admin, users, csrfToken(c), "Server error.").Render(ctx, c.Response())
	}

	u := &domain.User{
		Username:     username,
		Email:        email,
		PasswordHash: hash,
		IsAdmin:      c.FormValue("is_admin") == "true",
	}
	if _, err := h.users.Create(ctx, u); err != nil {
		users, _ := h.users.List(ctx)
		return pages.AdminUsers(admin, users, csrfToken(c), "Could not create user. Email or username may already be taken.").Render(ctx, c.Response())
	}

	return redirect(c, "/admin/users")
}

func (h *UsersHandler) Delete(c *echo.Context) error {
	ctx := c.Request().Context()
	admin := currentUser(c)

	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.ErrNotFound
	}

	// Prevent self-deletion
	if userID == admin.ID {
		return echo.ErrForbidden
	}

	if err := h.users.Delete(ctx, userID); err != nil {
		return echo.ErrInternalServerError
	}

	if isHTMX(c) {
		return c.NoContent(200)
	}
	return redirect(c, "/admin/users")
}
