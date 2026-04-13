package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

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
	user := currentUser(c)

	galleryID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.ErrNotFound
	}

	gallery, err := h.galleries.GetByID(ctx, galleryID)
	if err != nil || gallery == nil {
		return echo.ErrNotFound
	}

	// Check upload permission: owner or editor member
	if gallery.OwnerID != user.ID {
		member, err := h.galleries.GetMember(ctx, galleryID, user.ID)
		if err != nil || member == nil || member.Role != domain.RoleEditor {
			return echo.ErrForbidden
		}
	}

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
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			continue
		}
		if err := h.processUpload(ctx, gallery, user.ID, fh.Filename, data); err != nil {
			continue
		}
		uploaded++
	}

	if uploaded == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "No valid files could be uploaded"})
	}

	if isHTMX(c) {
		c.Response().Header().Set("HX-Redirect", "/admin/galleries/"+galleryID.String())
		return c.NoContent(http.StatusOK)
	}
	return c.Redirect(http.StatusFound, "/admin/galleries/"+galleryID.String())
}

func (h *UploadHandler) processUpload(ctx context.Context, gallery *domain.Gallery, uploaderID uuid.UUID, filename string, data []byte) error {
	// Detect MIME from file content
	mimeType, combinedReader, err := media.DetectMIME(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("detect mime: %w", err)
	}
	// Re-read full data after MIME detection
	fullData, err := io.ReadAll(combinedReader)
	if err != nil {
		return err
	}
	data = fullData

	if !media.IsAllowedMIME(mimeType) {
		return fmt.Errorf("unsupported file type: %s", mimeType)
	}

	photoID := uuid.New()

	// Determine extension
	ext := extensionForMIME(mimeType)
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(filename))
		if ext == "" {
			ext = ".bin"
		}
	}

	sortOrder, _ := h.photos.GetNextSortOrder(ctx, gallery.ID)
	fileSize := int64(len(data))

	if strings.HasPrefix(mimeType, "video/") {
		return h.processVideoUpload(ctx, gallery, uploaderID, filename, data, mimeType, ext, photoID, fileSize, sortOrder)
	}
	return h.processImageUpload(ctx, gallery, uploaderID, filename, data, mimeType, ext, photoID, fileSize, sortOrder)
}

func (h *UploadHandler) processImageUpload(
	ctx context.Context,
	gallery *domain.Gallery,
	uploaderID uuid.UUID,
	filename string,
	data []byte,
	mimeType, ext string,
	photoID uuid.UUID,
	fileSize int64,
	sortOrder int,
) error {
	// Extract metadata
	meta := media.ExtractMetadata(data, mimeType)

	// Aspect ratio 360 fallback
	if !meta.Is360 && meta.Width > 0 && meta.Height > 0 {
		if media.Detect360FromAspectRatio(meta.Width, meta.Height) {
			meta.Is360 = true
			meta.ProjectionType = "equirectangular"
		}
	}

	// Save original
	storagePath, err := h.processor.SaveOriginal(gallery.ID, photoID, data, ext)
	if err != nil {
		return fmt.Errorf("save original: %w", err)
	}

	// Generate thumbnail
	thumbPath, width, height, err := h.processor.GenerateThumbnail(gallery.ID, photoID, data, meta.Is360)
	if err != nil {
		thumbPath = storagePath
		width = 0
		height = 0
	}

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

func (h *UploadHandler) processVideoUpload(
	ctx context.Context,
	gallery *domain.Gallery,
	uploaderID uuid.UUID,
	filename string,
	data []byte,
	mimeType, ext string,
	photoID uuid.UUID,
	fileSize int64,
	sortOrder int,
) error {
	// Save original first (ffprobe/ffmpeg need a real file path)
	storagePath, err := h.processor.SaveOriginal(gallery.ID, photoID, data, ext)
	if err != nil {
		return fmt.Errorf("save video original: %w", err)
	}

	absPath := h.processor.ServeOriginalPath(storagePath)

	// Extract video metadata via ffprobe
	vmeta, err := media.ExtractVideoMeta(absPath)
	if err != nil {
		// Non-fatal — store without dimensions/duration/360 info
		vmeta = &media.VideoMeta{}
	}

	// Generate thumbnail frame
	thumbPath, err := h.processor.GenerateVideoThumbnail(gallery.ID, photoID, absPath, vmeta.Is360)
	if err != nil {
		// Fall back to using the original path as thumb (no preview)
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
	user := currentUser(c)

	galleryID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.ErrNotFound
	}

	gallery, err := h.galleries.GetByID(ctx, galleryID)
	if err != nil || gallery == nil {
		return echo.ErrNotFound
	}
	if gallery.OwnerID != user.ID {
		member, _ := h.galleries.GetMember(ctx, galleryID, user.ID)
		if member == nil || member.Role != domain.RoleEditor {
			return echo.ErrForbidden
		}
	}

	photoID, err := uuid.Parse(c.Param("pid"))
	if err != nil {
		return echo.ErrNotFound
	}

	photo, err := h.photos.GetByID(ctx, photoID)
	if err != nil || photo == nil || photo.GalleryID != galleryID {
		return echo.ErrNotFound
	}

	// Delete files from disk
	h.processor.DeletePhoto(photo.StoragePath, photo.ThumbPath)

	if err := h.photos.Delete(ctx, photoID); err != nil {
		return echo.ErrInternalServerError
	}

	if isHTMX(c) {
		return c.NoContent(http.StatusOK)
	}
	return c.Redirect(http.StatusFound, "/admin/galleries/"+galleryID.String())
}

func (h *UploadHandler) UpdatePhotoMeta(c *echo.Context) error {
	ctx := c.Request().Context()
	user := currentUser(c)

	galleryID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.ErrNotFound
	}

	gallery, err := h.galleries.GetByID(ctx, galleryID)
	if err != nil || gallery == nil {
		return echo.ErrNotFound
	}
	if gallery.OwnerID != user.ID {
		member, _ := h.galleries.GetMember(ctx, galleryID, user.ID)
		if member == nil || member.Role != domain.RoleEditor {
			return echo.ErrForbidden
		}
	}

	photoID, err := uuid.Parse(c.Param("pid"))
	if err != nil {
		return echo.ErrNotFound
	}

	photo, err := h.photos.GetByID(ctx, photoID)
	if err != nil || photo == nil || photo.GalleryID != galleryID {
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
	return c.Redirect(http.StatusFound, "/admin/galleries/"+galleryID.String())
}

func (h *UploadHandler) ReorderPhotos(c *echo.Context) error {
	ctx := c.Request().Context()
	user := currentUser(c)

	galleryID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.ErrNotFound
	}

	gallery, err := h.galleries.GetByID(ctx, galleryID)
	if err != nil || gallery == nil {
		return echo.ErrNotFound
	}
	if gallery.OwnerID != user.ID {
		member, _ := h.galleries.GetMember(ctx, galleryID, user.ID)
		if member == nil || member.Role != domain.RoleEditor {
			return echo.ErrForbidden
		}
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
		_ = h.photos.Reorder(ctx, galleryID, orderedIDs)
	}

	return c.NoContent(http.StatusOK)
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
