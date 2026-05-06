package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/openscape/openscape/internal/domain"
	"github.com/openscape/openscape/internal/media"
	"github.com/openscape/openscape/internal/repository"
	"github.com/openscape/openscape/internal/worker"
	"github.com/openscape/openscape/web/templates/components"
)

type UploadHandler struct {
	galleries   *repository.GalleryStore
	photos      *repository.PhotoStore
	processor   *media.Processor
	workers     *worker.Pool
	maxUploadMB int64
}

func NewUploadHandler(
	galleries *repository.GalleryStore,
	photos *repository.PhotoStore,
	processor *media.Processor,
	workers *worker.Pool,
	maxUploadMB int64,
) *UploadHandler {
	return &UploadHandler{
		galleries:   galleries,
		photos:      photos,
		processor:   processor,
		workers:     workers,
		maxUploadMB: maxUploadMB,
	}
}

// Upload streams a multipart upload part-by-part — never buffers the whole
// request body. Each file is written straight to disk; the photo row is
// created with status='processing' and a job is enqueued for the worker pool
// to thumbnail/ffprobe in the background. The HTTP response returns as soon
// as all parts are durably on disk, so the client doesn't wait on ffmpeg.
func (h *UploadHandler) Upload(c *echo.Context) error {
	gallery, err := requireGalleryEditor(c, h.galleries)
	if err != nil {
		return err
	}
	user := currentUser(c)

	maxBytes := h.maxUploadMB * 1024 * 1024
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxBytes)

	mr, err := c.Request().MultipartReader()
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "expected multipart upload"})
	}

	ctx := c.Request().Context()
	var uploaded int
	var lastErr string

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			lastErr = "could not read upload part: " + err.Error()
			break
		}
		// Skip non-file form fields (the CSRF token rides as a header on this
		// endpoint, but allow it in the body too).
		if part.FormName() != "photos" || part.FileName() == "" {
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
			continue
		}

		if err := h.processPart(ctx, gallery, user.ID, part.FileName(), part); err != nil {
			lastErr = err.Error()
			_ = part.Close()
			continue
		}
		_ = part.Close()
		uploaded++
	}

	if uploaded == 0 {
		msg := "No valid files could be uploaded"
		if lastErr != "" {
			msg = lastErr
		}
		return c.JSON(http.StatusBadRequest, map[string]string{"error": msg})
	}

	if isHTMX(c) {
		c.Response().Header().Set("HX-Redirect", "/admin/galleries/"+gallery.ID.String())
		return c.NoContent(http.StatusOK)
	}
	return c.Redirect(http.StatusFound, "/admin/galleries/"+gallery.ID.String())
}

// processPart streams one multipart part to disk, creates a 'processing' photo
// row, and enqueues the worker job. No decoding/probing happens here.
func (h *UploadHandler) processPart(ctx context.Context, gallery *domain.Gallery, uploaderID uuid.UUID, filename string, part io.Reader) error {
	// Peek at the first 512 bytes for MIME sniffing without buffering the rest.
	peek := make([]byte, 512)
	n, err := io.ReadFull(part, peek)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return fmt.Errorf("read peek: %w", err)
	}
	peek = peek[:n]

	mimeType := http.DetectContentType(peek)

	// Content sniffing returns octet-stream for QuickTime MOV and some H.265
	// MP4 variants. Fall back to extension so legitimate videos aren't refused.
	if mimeType == "application/octet-stream" || mimeType == "application/zip" {
		ext := strings.ToLower(filepath.Ext(filename))
		if m := media.MIMEFromExtension(ext); m != "" {
			mimeType = m
		}
	}
	if !media.IsAllowedMIME(mimeType) {
		// Still drain the reader so MultipartReader can advance.
		_, _ = io.Copy(io.Discard, part)
		return fmt.Errorf("unsupported file type: %s", mimeType)
	}

	ext := extensionForMIME(mimeType)
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(filename))
		if ext == "" {
			ext = ".bin"
		}
	}

	photoID := uuid.New()
	full := io.MultiReader(bytes.NewReader(peek), part)

	storagePath, fileSize, err := h.processor.SaveOriginalFromReader(gallery.ID, photoID, full, ext)
	if err != nil {
		return fmt.Errorf("save original: %w", err)
	}

	sortOrder, _ := h.photos.GetNextSortOrder(ctx, gallery.ID)

	// Use the original storage path as a placeholder thumbnail until the worker
	// generates a real one. ThumbPath is NOT NULL in the schema. The UI hides
	// the thumb image when status=processing, so the placeholder isn't shown.
	p := &domain.Photo{
		ID:          photoID,
		GalleryID:   gallery.ID,
		UploadedBy:  uploaderID,
		Filename:    filename,
		StoragePath: storagePath,
		ThumbPath:   storagePath,
		FileSize:    &fileSize,
		MimeType:    mimeType,
		ExifData:    map[string]any{},
		SortOrder:   sortOrder,
		Status:      domain.PhotoStatusProcessing,
	}
	if _, err := h.photos.Create(ctx, p); err != nil {
		// Roll back the on-disk file so we don't accumulate orphans.
		_ = os.Remove(h.processor.ServeOriginalPath(storagePath))
		return fmt.Errorf("create photo row: %w", err)
	}

	h.workers.Enqueue(worker.Job{PhotoID: p.ID})
	slog.Debug("upload accepted", "photo_id", p.ID, "filename", filename, "bytes", fileSize)
	return nil
}

func (h *UploadHandler) DeletePhoto(c *echo.Context) error {
	ctx := c.Request().Context()

	gallery, err := requireGalleryEditor(c, h.galleries)
	if err != nil {
		return err
	}

	photoID, err := uuid.Parse(c.Param("pid"))
	if err != nil {
		return echo.ErrNotFound
	}

	photo, err := h.photos.GetByID(ctx, photoID)
	if err != nil || photo == nil || photo.GalleryID != gallery.ID {
		return echo.ErrNotFound
	}

	h.processor.DeletePhoto(photo.StoragePath, photo.ThumbPath)

	if err := h.photos.Delete(ctx, photoID); err != nil {
		return echo.ErrInternalServerError
	}

	if isHTMX(c) {
		return c.NoContent(http.StatusOK)
	}
	return c.Redirect(http.StatusFound, "/admin/galleries/"+gallery.ID.String())
}

func (h *UploadHandler) UpdatePhotoMeta(c *echo.Context) error {
	ctx := c.Request().Context()

	gallery, err := requireGalleryEditor(c, h.galleries)
	if err != nil {
		return err
	}

	photoID, err := uuid.Parse(c.Param("pid"))
	if err != nil {
		return echo.ErrNotFound
	}

	photo, err := h.photos.GetByID(ctx, photoID)
	if err != nil || photo == nil || photo.GalleryID != gallery.ID {
		return echo.ErrNotFound
	}

	photo.Title = c.FormValue("title")
	photo.Description = c.FormValue("description")

	if err := h.photos.Update(ctx, photo); err != nil {
		return echo.ErrInternalServerError
	}

	if isHTMX(c) {
		return c.NoContent(http.StatusOK)
	}
	return c.Redirect(http.StatusFound, "/admin/galleries/"+gallery.ID.String())
}

func (h *UploadHandler) ReorderPhotos(c *echo.Context) error {
	ctx := c.Request().Context()

	gallery, err := requireGalleryEditor(c, h.galleries)
	if err != nil {
		return err
	}

	if err := c.Request().ParseForm(); err != nil {
		return c.NoContent(http.StatusBadRequest)
	}
	ids := c.Request().Form["order[]"]
	var orderedIDs []uuid.UUID
	for _, idStr := range ids {
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		orderedIDs = append(orderedIDs, id)
	}

	if len(orderedIDs) > 0 {
		if err := h.photos.Reorder(ctx, gallery.ID, orderedIDs); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "reorder failed"})
		}
	}

	return c.NoContent(http.StatusOK)
}

func (h *UploadHandler) SortByDate(c *echo.Context) error {
	ctx := c.Request().Context()

	gallery, err := requireGalleryEditor(c, h.galleries)
	if err != nil {
		return err
	}

	desc := c.FormValue("direction") != "asc" // default is desc (newest first)
	if err := h.photos.SortByDate(ctx, gallery.ID, desc); err != nil {
		return echo.ErrInternalServerError
	}

	return redirect(c, "/admin/galleries/"+gallery.ID.String())
}

// PhotoStatus returns a tiny templ partial reflecting the current processing
// state of a photo. The card polls this endpoint while status=processing.
func (h *UploadHandler) PhotoStatus(c *echo.Context) error {
	ctx := c.Request().Context()

	gallery, err := requireGalleryEditor(c, h.galleries)
	if err != nil {
		return err
	}

	photoID, err := uuid.Parse(c.Param("pid"))
	if err != nil {
		return echo.ErrNotFound
	}

	photo, err := h.photos.GetByID(ctx, photoID)
	if err != nil || photo == nil || photo.GalleryID != gallery.ID {
		return echo.ErrNotFound
	}

	return components.PhotoCard(photo, gallery.Slug, true, csrfToken(c)).Render(ctx, c.Response())
}

func extensionForMIME(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/heic", "image/heif":
		return ".heic"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	case "video/ogg":
		return ".ogv"
	case "video/x-msvideo":
		return ".avi"
	}
	return ""
}
