// Package worker runs thumbnail generation and metadata extraction off the
// HTTP request goroutine. The upload handler enqueues a job once the original
// is durably on disk; workers pick it up, generate a thumbnail (and ffprobe
// videos), then flip the photo row from 'processing' to 'ready' or 'failed'.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openscape/openscape/internal/domain"
	"github.com/openscape/openscape/internal/media"
	"github.com/openscape/openscape/internal/repository"
)

const (
	ffprobeTimeout = 60 * time.Second
	ffmpegTimeout  = 5 * time.Minute
	imageTimeout   = 2 * time.Minute
)

type Job struct {
	PhotoID uuid.UUID
}

type Pool struct {
	jobs      chan Job
	processor *media.Processor
	photos    *repository.PhotoStore
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// New starts `workers` goroutines (defaulting to NumCPU). The returned pool
// accepts jobs via Enqueue; call Shutdown to drain on exit.
func New(processor *media.Processor, photos *repository.PhotoStore, workers, queueDepth int) *Pool {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if queueDepth <= 0 {
		queueDepth = workers * 8
	}
	p := &Pool{
		jobs:      make(chan Job, queueDepth),
		processor: processor,
		photos:    photos,
	}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.run()
	}
	slog.Info("worker pool started", "workers", workers, "queue_depth", queueDepth)
	return p
}

// Enqueue submits a job. Non-blocking under normal load; when the queue is
// full it blocks the caller, providing natural backpressure on uploads.
func (p *Pool) Enqueue(j Job) {
	p.jobs <- j
}

// Shutdown stops accepting new work and waits for in-flight jobs to finish or
// for the context to expire, whichever comes first. Safe to call multiple times.
func (p *Pool) Shutdown(ctx context.Context) {
	p.closeOnce.Do(func() { close(p.jobs) })
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		slog.Warn("worker pool shutdown timed out", "remaining", len(p.jobs))
	}
}

func (p *Pool) run() {
	defer p.wg.Done()
	for j := range p.jobs {
		p.process(j)
	}
}

func (p *Pool) process(j Job) {
	// Use a fresh context so a slow ffmpeg run isn't bounded by the original
	// request's deadline (the request returned long ago).
	ctx, cancel := context.WithTimeout(context.Background(), ffmpegTimeout+ffprobeTimeout+imageTimeout)
	defer cancel()

	photo, err := p.photos.GetByID(ctx, j.PhotoID)
	if err != nil || photo == nil {
		slog.Error("worker: photo not found", "photo_id", j.PhotoID, "error", err)
		return
	}

	if err := p.processPhoto(ctx, photo); err != nil {
		reason := err.Error()
		if len(reason) > 500 {
			reason = reason[:500]
		}
		if e := p.photos.MarkFailed(ctx, photo.ID, reason); e != nil {
			slog.Error("worker: mark failed", "photo_id", photo.ID, "error", e)
		}
		slog.Warn("worker: processing failed", "photo_id", photo.ID, "filename", photo.Filename, "error", err)
		return
	}

	if err := p.photos.UpdateProcessed(ctx, photo); err != nil {
		slog.Error("worker: update processed", "photo_id", photo.ID, "error", err)
		return
	}
	slog.Debug("worker: processed", "photo_id", photo.ID, "is_video", photo.IsVideo())
}

func (p *Pool) processPhoto(ctx context.Context, photo *domain.Photo) error {
	absPath := p.processor.ServeOriginalPath(photo.StoragePath)
	if _, err := os.Stat(absPath); err != nil {
		return err
	}

	if strings.HasPrefix(photo.MimeType, "video/") {
		return p.processVideo(ctx, photo, absPath)
	}
	return p.processImage(ctx, photo, absPath)
}

func (p *Pool) processImage(ctx context.Context, photo *domain.Photo, absPath string) error {
	imgCtx, cancel := context.WithTimeout(ctx, imageTimeout)
	defer cancel()

	// Reading metadata wants the bytes; reading thumbnails wants the decoded
	// image. Read once via os.ReadFile rather than hammering disk twice — for
	// a 100 MB equirectangular PNG this is still bounded by MaxUpload.
	data, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	if imgCtx.Err() != nil {
		return imgCtx.Err()
	}

	meta := media.ExtractMetadata(data, photo.MimeType)
	if !meta.Is360 && meta.Width > 0 && meta.Height > 0 {
		if media.Detect360FromAspectRatio(meta.Width, meta.Height) {
			meta.Is360 = true
			meta.ProjectionType = "equirectangular"
		}
	}

	thumbPath, width, height, err := p.processor.GenerateThumbnailFromFile(photo.GalleryID, photo.ID, absPath, meta.Is360)
	if err != nil {
		// Fall back to bytes path (covers HEIC/HEIF and any imaging.Open quirks).
		thumbPath, width, height, err = p.processor.GenerateThumbnail(photo.GalleryID, photo.ID, data, meta.Is360)
		if err != nil {
			return err
		}
	}

	photo.ThumbPath = thumbPath
	if width > 0 {
		photo.Width = &width
		photo.Height = &height
	}
	if meta.CapturedAt != nil {
		photo.CapturedAt = meta.CapturedAt
	}
	if meta.Is360 {
		photo.Is360 = true
		if meta.ProjectionType != "" {
			proj := meta.ProjectionType
			photo.ProjectionType = &proj
		}
	}
	return nil
}

func (p *Pool) processVideo(ctx context.Context, photo *domain.Photo, absPath string) error {
	probeCtx, cancelProbe := context.WithTimeout(ctx, ffprobeTimeout)
	defer cancelProbe()

	vmeta, err := media.ExtractVideoMeta(probeCtx, absPath)
	if err != nil {
		// ffprobe may fail entirely (binary missing, container weird) — keep
		// going so we still get a placeholder thumbnail.
		vmeta = &media.VideoMeta{}
		slog.Debug("ffprobe failed; falling back to placeholder thumb", "photo_id", photo.ID, "error", err)
	}

	thumbCtx, cancelThumb := context.WithTimeout(ctx, ffmpegTimeout)
	defer cancelThumb()

	thumbPath, err := p.processor.GenerateVideoThumbnail(thumbCtx, photo.GalleryID, photo.ID, absPath, vmeta.Is360)
	if err != nil {
		// GenerateVideoThumbnail's contract is to always return *something*,
		// so a non-nil error here means even the placeholder couldn't be
		// written — surface as a real failure.
		if errors.Is(err, context.DeadlineExceeded) {
			return errors.New("video thumbnail generation timed out")
		}
		return err
	}

	photo.ThumbPath = thumbPath
	if vmeta.Width > 0 {
		photo.Width = &vmeta.Width
		photo.Height = &vmeta.Height
	}
	if vmeta.Duration > 0 {
		photo.Duration = &vmeta.Duration
	}
	if vmeta.CapturedAt != nil {
		photo.CapturedAt = vmeta.CapturedAt
	}
	if vmeta.Is360 {
		photo.Is360 = true
		proj := "equirectangular"
		photo.ProjectionType = &proj
	}
	return nil
}
