package handler

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/openscape/openscape/internal/domain"
	"github.com/openscape/openscape/internal/media"
	"github.com/openscape/openscape/internal/repository"
)

// ServeUpload serves photo files with access control.
// URL pattern: /uploads/:gallery_id/originals/:filename or /uploads/:gallery_id/thumbs/:filename
// This handler performs its own gallery access check by gallery_id.
func ServeUpload(
	processor *media.Processor,
	galleries *repository.GalleryStore,
	galSessions *repository.GallerySessionStore,
) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()

		galleryIDStr := c.Param("gallery_id")
		rest := c.Param("*")

		// Sanitize: no path traversal
		if strings.Contains(rest, "..") || strings.Contains(galleryIDStr, "..") {
			return echo.ErrNotFound
		}

		galleryID, err := uuid.Parse(galleryIDStr)
		if err != nil {
			return echo.ErrNotFound
		}

		galByID, err := galleries.GetByID(ctx, galleryID)
		if err != nil || galByID == nil {
			return echo.ErrNotFound
		}

		// Access check (same logic as gallery middleware)
		user, _ := c.Get("user").(*domain.User)

		if err := checkGalleryAccessByGallery(c, galByID, user, galleries, galSessions); err != nil {
			return err
		}

		// Parse path type
		parts := strings.SplitN(strings.TrimPrefix(rest, "/"), "/", 2)
		if len(parts) != 2 {
			return echo.ErrNotFound
		}
		fileType := parts[0]
		filename := filepath.Base(parts[1]) // sanitize: take only the basename

		if filename == "." || filename == "" {
			return echo.ErrNotFound
		}

		relPath := filepath.Join(galleryIDStr, fileType, filename)

		var fsPath string
		switch fileType {
		case "originals":
			fsPath = processor.ServeOriginalPath(relPath)
		case "thumbs":
			fsPath = processor.ServeThumbPath(relPath)
		default:
			return echo.ErrNotFound
		}

		c.Response().Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeFile(c.Response(), c.Request(), fsPath)
		return nil
	}
}

func checkGalleryAccessByGallery(
	c *echo.Context,
	gallery *domain.Gallery,
	user *domain.User,
	galleries *repository.GalleryStore,
	galSessions *repository.GallerySessionStore,
) error {
	ctx := c.Request().Context()

	// Owner always has access
	if user != nil && (gallery.OwnerID == user.ID || user.IsAdmin) {
		return nil
	}

	switch gallery.Visibility {
	case domain.VisibilityPublic, domain.VisibilityUnlisted:
		return nil

	case domain.VisibilityUnlistedProtected:
		cookieName := domain.GalSessionCookiePrefix + gallery.Slug
		cookie, err := c.Cookie(cookieName)
		if err != nil || cookie.Value == "" {
			return echo.ErrForbidden
		}
		gs, err := galSessions.GetByGallery(ctx, cookie.Value, gallery.ID)
		if err != nil || gs == nil {
			return echo.ErrForbidden
		}
		return nil

	case domain.VisibilityPrivate:
		if user == nil {
			return echo.ErrUnauthorized
		}
		member, err := galleries.GetMember(ctx, gallery.ID, user.ID)
		if err != nil || member == nil {
			return echo.ErrForbidden
		}
		return nil
	}
	return echo.ErrForbidden
}
