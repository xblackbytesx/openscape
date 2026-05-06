package handler

import (
	"context"
	"errors"
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
	"github.com/tus/tusd/v2/pkg/filestore"
	tusd "github.com/tus/tusd/v2/pkg/handler"
)

// TusHandler exposes a tus.io resumable-upload endpoint shared across all
// galleries. Authentication is enforced by Echo middleware before requests
// reach Mount. The gallery-editor check happens in PreCreate, which reads the
// galleryID supplied via Upload-Metadata and the trusted user ID from the
// request context. Once an upload is created its metadata is immutable, so
// PATCH/HEAD/DELETE on subsequent chunks only need the auth check.
type TusHandler struct {
	tus        *tusd.Handler
	stagingDir string
	galleries  *repository.GalleryStore
	photos     *repository.PhotoStore
	processor  *media.Processor
	workers    *worker.Pool
}

type tusCtxKey struct{ k string }

var tusCtxUser = tusCtxKey{"user"}

// NewTusHandler returns a configured tusd handler rooted at urlPrefix.
// stagingDir holds in-progress chunks; finished files are moved out by
// the PostFinish loop.
func NewTusHandler(
	urlPrefix string,
	uploadsPath string,
	galleries *repository.GalleryStore,
	photos *repository.PhotoStore,
	processor *media.Processor,
	workers *worker.Pool,
) (*TusHandler, error) {
	stagingDir := filepath.Join(uploadsPath, ".tus")
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return nil, err
	}

	store := filestore.New(stagingDir)
	composer := tusd.NewStoreComposer()
	store.UseIn(composer)

	h := &TusHandler{
		stagingDir: stagingDir,
		galleries:  galleries,
		photos:     photos,
		processor:  processor,
		workers:    workers,
	}

	tusHandler, err := tusd.NewHandler(tusd.Config{
		BasePath:                urlPrefix,
		StoreComposer:           composer,
		MaxSize:                 0,
		NotifyCompleteUploads:   true,
		NotifyTerminatedUploads: true,
		RespectForwardedHeaders: true,
		PreUploadCreateCallback: h.preCreate,
	})
	if err != nil {
		return nil, err
	}
	h.tus = tusHandler

	go h.consumeHooks()
	return h, nil
}

// Mount delegates the request to tusd. The Echo group has already verified
// the user is authenticated; we stamp the trusted user ID onto the request
// context so PreCreate can look it up without trusting any client-supplied
// header. PATCH/HEAD/DELETE for an existing upload don't re-check gallery
// membership since the binding was made at CREATE time.
func (h *TusHandler) Mount(c *echo.Context) error {
	user := currentUser(c)
	if user == nil {
		return echo.ErrUnauthorized
	}
	r := c.Request()
	ctx := context.WithValue(r.Context(), tusCtxUser, user.ID.String())
	h.tus.ServeHTTP(c.Response(), r.WithContext(ctx))
	return nil
}

// preCreate runs once per upload, on the initial POST. It reads the trusted
// user ID from the request context (propagated to hook.Context by tusd),
// then validates that this user is an editor of the gallery whose ID the
// client put in Upload-Metadata.
func (h *TusHandler) preCreate(hook tusd.HookEvent) (tusd.HTTPResponse, tusd.FileInfoChanges, error) {
	ctx := hook.Context
	if ctx == nil {
		ctx = context.Background()
	}
	uploaderStr, _ := ctx.Value(tusCtxUser).(string)
	if uploaderStr == "" {
		return tusd.HTTPResponse{StatusCode: http.StatusUnauthorized},
			tusd.FileInfoChanges{},
			errors.New("unauthenticated tus upload")
	}
	uploaderID, err := uuid.Parse(uploaderStr)
	if err != nil {
		return tusd.HTTPResponse{StatusCode: http.StatusUnauthorized},
			tusd.FileInfoChanges{}, err
	}

	galleryStr := hook.Upload.MetaData["galleryID"]
	galleryID, err := uuid.Parse(galleryStr)
	if err != nil {
		return tusd.HTTPResponse{StatusCode: http.StatusBadRequest},
			tusd.FileInfoChanges{},
			errors.New("missing or invalid galleryID metadata")
	}

	gallery, err := h.galleries.GetByID(ctx, galleryID)
	if err != nil || gallery == nil {
		return tusd.HTTPResponse{StatusCode: http.StatusNotFound},
			tusd.FileInfoChanges{},
			errors.New("gallery not found")
	}
	if gallery.OwnerID != uploaderID {
		member, err := h.galleries.GetMember(ctx, galleryID, uploaderID)
		if err != nil || member == nil || member.Role != domain.RoleEditor {
			return tusd.HTTPResponse{StatusCode: http.StatusForbidden},
				tusd.FileInfoChanges{},
				errors.New("not an editor of this gallery")
		}
	}

	mime := hook.Upload.MetaData["filetype"]
	filename := hook.Upload.MetaData["filename"]
	if mime == "" || mime == "application/octet-stream" {
		if m := media.MIMEFromExtension(strings.ToLower(filepath.Ext(filename))); m != "" {
			mime = m
		}
	}
	if !media.IsAllowedMIME(mime) {
		return tusd.HTTPResponse{StatusCode: http.StatusUnsupportedMediaType},
			tusd.FileInfoChanges{},
			errors.New("unsupported file type: " + mime)
	}

	return tusd.HTTPResponse{}, tusd.FileInfoChanges{
		MetaData: map[string]string{
			"galleryID":  galleryID.String(),
			"uploaderID": uploaderID.String(),
			"filename":   filename,
			"filetype":   mime,
		},
	}, nil
}

func (h *TusHandler) consumeHooks() {
	for {
		select {
		case ev, ok := <-h.tus.CompleteUploads:
			if !ok {
				return
			}
			if err := h.finishUpload(context.Background(), ev); err != nil {
				slog.Error("tus: finish upload failed", "id", ev.Upload.ID, "error", err)
				h.cleanupStaging(ev.Upload.ID)
			}
		case ev, ok := <-h.tus.TerminatedUploads:
			if !ok {
				return
			}
			slog.Debug("tus: upload terminated", "id", ev.Upload.ID)
		}
	}
}

func (h *TusHandler) cleanupStaging(uploadID string) {
	stagedPath := filepath.Join(h.stagingDir, uploadID)
	_ = os.Remove(stagedPath)
	_ = os.Remove(stagedPath + ".info")
}

func (h *TusHandler) finishUpload(ctx context.Context, ev tusd.HookEvent) error {
	meta := ev.Upload.MetaData
	galleryID, err := uuid.Parse(meta["galleryID"])
	if err != nil {
		return errors.New("missing galleryID metadata")
	}
	uploaderID, err := uuid.Parse(meta["uploaderID"])
	if err != nil {
		return errors.New("missing uploaderID metadata")
	}

	mimeType := meta["filetype"]
	filename := meta["filename"]
	if !media.IsAllowedMIME(mimeType) {
		return errors.New("unsupported file type: " + mimeType)
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		ext = extensionForMIME(mimeType)
	}
	if ext == "" {
		ext = ".bin"
	}

	stagedPath := filepath.Join(h.stagingDir, ev.Upload.ID)
	photoID := uuid.New()
	dstDir := filepath.Join(h.processor.UploadsRoot(), galleryID.String(), "originals")
	if err := os.MkdirAll(dstDir, 0o700); err != nil {
		return err
	}
	dstName := photoID.String() + ext
	dstPath := filepath.Join(dstDir, dstName)
	if err := os.Rename(stagedPath, dstPath); err != nil {
		// Cross-device rename can fail with EXDEV on bind mounts; copy + remove instead.
		if cpErr := copyFile(stagedPath, dstPath); cpErr != nil {
			return cpErr
		}
		_ = os.Remove(stagedPath)
	}
	_ = os.Chmod(dstPath, 0o600)
	_ = os.Remove(stagedPath + ".info")

	storagePath := filepath.Join(galleryID.String(), "originals", dstName)
	fi, err := os.Stat(dstPath)
	if err != nil {
		return err
	}
	fileSize := fi.Size()

	sortOrder, _ := h.photos.GetNextSortOrder(ctx, galleryID)
	p := &domain.Photo{
		ID:          photoID,
		GalleryID:   galleryID,
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
		_ = os.Remove(dstPath)
		return err
	}
	h.workers.Enqueue(worker.Job{PhotoID: p.ID})
	slog.Debug("tus upload finalised", "photo_id", p.ID, "filename", filename, "bytes", fileSize)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
