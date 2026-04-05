package handler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/openscape/openscape/internal/auth"
	"github.com/openscape/openscape/internal/domain"
	"github.com/openscape/openscape/internal/repository"
	"github.com/openscape/openscape/web/templates/pages"
)

type GalleryHandler struct {
	galleries     *repository.GalleryStore
	photos        *repository.PhotoStore
	galSessions   *repository.GallerySessionStore
	secureCookies bool
}

func NewGalleryHandler(
	galleries *repository.GalleryStore,
	photos *repository.PhotoStore,
	galSessions *repository.GallerySessionStore,
	secureCookies bool,
) *GalleryHandler {
	return &GalleryHandler{galleries: galleries, photos: photos, galSessions: galSessions, secureCookies: secureCookies}
}

func (h *GalleryHandler) View(c *echo.Context) error {
	ctx := c.Request().Context()
	gallery := currentGallery(c)
	user := currentUser(c)

	photoList, err := h.photos.ListByGallery(ctx, gallery.ID)
	if err != nil {
		photoList = []*domain.Photo{}
	}

	edit := canEdit(c)
	// Also allow if user is owner
	if user != nil && gallery.OwnerID == user.ID {
		edit = true
	}

	return pages.GalleryView(gallery, photoList, csrfToken(c), edit, user).Render(ctx, c.Response())
}

func (h *GalleryHandler) PhotoView(c *echo.Context) error {
	ctx := c.Request().Context()
	gallery := currentGallery(c)
	user := currentUser(c)

	photoIDStr := c.Param("id")
	photoID, err := uuid.Parse(photoIDStr)
	if err != nil {
		return echo.ErrNotFound
	}

	photo, err := h.photos.GetByID(ctx, photoID)
	if err != nil || photo == nil || photo.GalleryID != gallery.ID {
		return echo.ErrNotFound
	}

	edit := canEdit(c)
	if user != nil && gallery.OwnerID == user.ID {
		edit = true
	}

	return pages.PhotoView(gallery, photo, csrfToken(c), edit, user).Render(ctx, c.Response())
}

func (h *GalleryHandler) UnlockGet(c *echo.Context) error {
	ctx := c.Request().Context()
	slug := c.Param("slug")

	gallery, err := h.galleries.GetBySlug(ctx, slug)
	if err != nil || gallery == nil {
		return echo.ErrNotFound
	}
	if gallery.Visibility != domain.VisibilityUnlistedProtected {
		return c.Redirect(http.StatusFound, "/g/"+slug)
	}

	return pages.GalleryUnlock(gallery, csrfToken(c), "").Render(ctx, c.Response())
}

func (h *GalleryHandler) UnlockPost(c *echo.Context) error {
	ctx := c.Request().Context()
	slug := c.Param("slug")

	gallery, err := h.galleries.GetBySlug(ctx, slug)
	if err != nil || gallery == nil {
		return echo.ErrNotFound
	}
	if gallery.Visibility != domain.VisibilityUnlistedProtected {
		return c.Redirect(http.StatusFound, "/g/"+slug)
	}

	password := c.FormValue("password")
	if gallery.PasswordHash == nil || !auth.CheckPassword(*gallery.PasswordHash, password) {
		return pages.GalleryUnlock(gallery, csrfToken(c), "Incorrect password. Please try again.").Render(ctx, c.Response())
	}

	// Generate gallery session token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return pages.GalleryUnlock(gallery, csrfToken(c), "Server error. Please try again.").Render(ctx, c.Response())
	}
	token := hex.EncodeToString(tokenBytes)

	gs := &domain.GallerySession{
		Token:     token,
		GalleryID: gallery.ID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	if err := h.galSessions.Create(ctx, gs); err != nil {
		return pages.GalleryUnlock(gallery, csrfToken(c), "Server error. Please try again.").Render(ctx, c.Response())
	}

	cookieName := fmt.Sprintf("openscape_gs_%s", slug)
	http.SetCookie(c.Response(), &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteStrictMode,
	})

	return c.Redirect(http.StatusFound, "/g/"+slug)
}

