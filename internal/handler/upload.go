package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/openscape/openscape/internal/domain"
	"github.com/openscape/openscape/internal/media"
	"github.com/openscape/openscape/internal/repository"
)

type UploadHandler struct {
	galleries   *repository.GalleryStore
	photos      *repository.PhotoStore
	processor   *media.Processor
	maxUploadMB int64
}

func NewUploadHandler(
	galleries *repository.GalleryStore,
	photos *repository.PhotoStore,
	processor *media.Processor,
	maxUploadMB int64,
) *UploadHandler {
	return &UploadHandler{
		galleries:   galleries,
		photos:      photos,
		processor:   processor,
		maxUploadMB: maxUploadMB,
	}
}

func (h *UploadHandler) Upload(c *echo.Context) error {
	ctx := c.Request().Context()

	gallery, err := requireGalleryEditor(c, h.galleries)
	if err != nil {
		return err
	}
	user := currentUser(c)

	// Limit total upload size
	maxBytes := h.maxUploadMB * 1024 * 1024
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxBytes)

	form, err := c.MultipartForm()
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Could not parse upload form"})
	}

	files := form.File["photos"]
	if len(files) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "No files provided"})
	}

	var uploaded int
	var lastErr string
	for _, fh := range files {
		if err := h.processFileHeader(ctx, gallery, user.ID, fh); err != nil {
			lastErr = err.Error()
			continue
		}
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

// processFileHeader handles one multipart file. Videos are streamed straight to
// disk (no full-file RAM buffer). Images are read into RAM for in-process decoding.
func (h *UploadHandler) processFileHeader(ctx context.Context, gallery *domain.Gallery, uploaderID uuid.UUID, fh *multipart.FileHeader) error {
	f, err := fh.Open()
	if err != nil {
		return fmt.Errorf("open upload: %w", err)
	}
	defer f.Close()

	// Peek at first 512 bytes for MIME sniffing without buffering the whole file.
	peek := make([]byte, 512)
	n, err := io.ReadFull(f, peek)
	if err != nil && err != io.ErrUnexpectedEOF {
		return fmt.Errorf("read peek: %w", err)
	}
	peek = peek[:n]

	mimeType := http.DetectContentType(peek)

	// Fallback: content sniffing returns octet-stream for many video containers
	// (e.g. QuickTime MOV, some H.265 MP4s). Use the file extension instead.
	if mimeType == "application/octet-stream" || mimeType == "application/zip" {
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if m := media.MIMEFromExtension(ext); m != "" {
			mimeType = m
		}
	}

	if !media.IsAllowedMIME(mimeType) {
		return fmt.Errorf("unsupported file type: %s", mimeType)
	}

	// Reconstruct a full reader: the peeked bytes + the rest of the file.
	full := io.MultiReader(bytes.NewReader(peek), f)

	photoID := uuid.New()
	ext := extensionForMIME(mimeType)
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(fh.Filename))
		if ext == "" {
			ext = ".bin"
		}
	}

	sortOrder, _ := h.photos.GetNextSortOrder(ctx, gallery.ID)

	if strings.HasPrefix(mimeType, "video/") {
		return h.processVideoStream(ctx, gallery, uploaderID, fh.Filename, mimeType, ext, photoID, sortOrder, full)
	}
	return h.processImageStream(ctx, gallery, uploaderID, fh.Filename, mimeType, ext, photoID, sortOrder, full)
}

func (h *UploadHandler) processImageStream(
	ctx context.Context,
	gallery *domain.Gallery,
	uploaderID uuid.UUID,
	filename, mimeType, ext string,
	photoID uuid.UUID,
	sortOrder int,
	r io.Reader,
) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read image: %w", err)
	}

	meta := media.ExtractMetadata(data, mimeType)
	if !meta.Is360 && meta.Width > 0 && meta.Height > 0 {
		if media.Detect360FromAspectRatio(meta.Width, meta.Height) {
			meta.Is360 = true
			meta.ProjectionType = "equirectangular"
		}
	}

	storagePath, err := h.processor.SaveOriginal(gallery.ID, photoID, data, ext)
	if err != nil {
		return fmt.Errorf("save original: %w", err)
	}

	thumbPath, width, height, err := h.processor.GenerateThumbnail(gallery.ID, photoID, data, meta.Is360)
	if err != nil {
		thumbPath = storagePath
		width, height = 0, 0
	}

	fileSize := int64(len(data))
	p := &domain.Photo{
		GalleryID:   gallery.ID,
		UploadedBy:  uploaderID,
		Filename:    filename,
		StoragePath: storagePath,
		ThumbPath:   thumbPath,
		FileSize:    &fileSize,
		MimeType:    mimeType,
		Is360:       meta.Is360,
		ExifData:    meta.ExifData,
		CapturedAt:  meta.CapturedAt,
		SortOrder:   sortOrder,
	}
	if meta.Is360 && meta.ProjectionType != "" {
		p.ProjectionType = &meta.ProjectionType
	}
	if width > 0 {
		p.Width = &width
		p.Height = &height
	}
	_, err = h.photos.Create(ctx, p)
	return err
}

func (h *UploadHandler) processVideoStream(
	ctx context.Context,
	gallery *domain.Gallery,
	uploaderID uuid.UUID,
	filename, mimeType, ext string,
	photoID uuid.UUID,
	sortOrder int,
	r io.Reader,
) error {
	// Stream directly to disk — never io.ReadAll the video into RAM.
	storagePath, fileSize, err := h.processor.SaveOriginalFromReader(gallery.ID, photoID, r, ext)
	if err != nil {
		return fmt.Errorf("save video: %w", err)
	}

	absPath := h.processor.ServeOriginalPath(storagePath)

	vmeta, err := media.ExtractVideoMeta(absPath)
	if err != nil {
		vmeta = &media.VideoMeta{}
	}

	// GenerateVideoThumbnail always returns a valid JPEG path (falls back to a
	// dark placeholder when ffmpeg is unavailable). Only fails on disk errors.
	thumbPath, err := h.processor.GenerateVideoThumbnail(gallery.ID, photoID, absPath, vmeta.Is360)
	if err != nil {
		// Can't write thumbnail at all — store video path so DB record is valid;
		// the card will show a broken image rather than panic.
		thumbPath = storagePath
	}

	p := &domain.Photo{
		GalleryID:   gallery.ID,
		UploadedBy:  uploaderID,
		Filename:    filename,
		StoragePath: storagePath,
		ThumbPath:   thumbPath,
		FileSize:    &fileSize,
		MimeType:    mimeType,
		Is360:       vmeta.Is360,
		ExifData:    map[string]any{},
		CapturedAt:  vmeta.CapturedAt,
		SortOrder:   sortOrder,
	}
	if vmeta.Is360 {
		proj := "equirectangular"
		p.ProjectionType = &proj
	}
	if vmeta.Width > 0 {
		p.Width = &vmeta.Width
		p.Height = &vmeta.Height
	}
	if vmeta.Duration > 0 {
		p.Duration = &vmeta.Duration
	}
	_, err = h.photos.Create(ctx, p)
	return err
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

	// Backfill captured_at for photos uploaded before EXIF extraction existed.
	if photos, err := h.photos.ListByGallery(ctx, gallery.ID); err == nil {
		for _, p := range photos {
			if p.CapturedAt != nil {
				continue
			}
			absPath := h.processor.ServeOriginalPath(p.StoragePath)
			var t *time.Time
			if strings.HasPrefix(p.MimeType, "video/") {
				if vmeta, err := media.ExtractVideoMeta(absPath); err == nil && vmeta.CapturedAt != nil {
					t = vmeta.CapturedAt
				}
			} else {
				if data, err := os.ReadFile(absPath); err == nil {
					imgMeta := media.ExtractMetadata(data, p.MimeType)
					t = imgMeta.CapturedAt
				}
			}
			if t != nil {
				_ = h.photos.UpdateCapturedAt(ctx, p.ID, *t)
			}
		}
	}

	desc := c.FormValue("direction") != "asc" // default is desc (newest first)
	if err := h.photos.SortByDate(ctx, gallery.ID, desc); err != nil {
		return echo.ErrInternalServerError
	}

	return redirect(c, "/admin/galleries/"+gallery.ID.String())
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
