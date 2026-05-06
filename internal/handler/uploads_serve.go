package handler

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/openscape/openscape/internal/auth"
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

		// Access check (shared with gallery view middleware)
		user, _ := c.Get("user").(*domain.User)
		cookieValue := ""
		if cookie, err := c.Cookie(domain.GalSessionCookiePrefix + galByID.Slug); err == nil {
			cookieValue = cookie.Value
		}
		access := auth.CheckGalleryAccess(ctx, galByID, user, cookieValue,
			func(ctx context.Context, token string) bool {
				gs, err := galSessions.GetByGallery(ctx, token, galByID.ID)
				return err == nil && gs != nil
			},
			func(ctx context.Context) *domain.GalleryMember {
				if user == nil {
					return nil
				}
				member, _ := galleries.GetMember(ctx, galByID.ID, user.ID)
				return member
			},
		)
		if !access.Allowed {
			if access.RequiresLogin {
				return echo.ErrUnauthorized
			}
			return echo.ErrForbidden
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

