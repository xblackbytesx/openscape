package media

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/disintegration/imaging"
	"github.com/google/uuid"
	_ "golang.org/x/image/webp"
)

type Processor struct {
	uploadsPath string
}

func NewProcessor(uploadsPath string) *Processor {
	return &Processor{uploadsPath: uploadsPath}
}

// SaveOriginal writes the raw file bytes to the originals directory.
// Returns the relative path (used as storage_path in DB).
func (p *Processor) SaveOriginal(galleryID, photoID uuid.UUID, data []byte, ext string) (string, error) {
	dir := filepath.Join(p.uploadsPath, galleryID.String(), "originals")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create originals dir: %w", err)
	}
	filename := photoID.String() + ext
	fullPath := filepath.Join(dir, filename)
	if err := os.WriteFile(fullPath, data, 0600); err != nil {
		return "", fmt.Errorf("write original: %w", err)
	}
	return filepath.Join(galleryID.String(), "originals", filename), nil
}

// SaveOriginalFromReader streams an upload directly from r to the originals
// directory without buffering the whole file in RAM. Returns the relative path
// and the number of bytes written.
func (p *Processor) SaveOriginalFromReader(galleryID, photoID uuid.UUID, r io.Reader, ext string) (string, int64, error) {
	dir := filepath.Join(p.uploadsPath, galleryID.String(), "originals")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", 0, fmt.Errorf("create originals dir: %w", err)
	}
	filename := photoID.String() + ext
	fullPath := filepath.Join(dir, filename)
	f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return "", 0, fmt.Errorf("create file: %w", err)
	}
	defer f.Close()
	n, err := io.Copy(f, r)
	if err != nil {
		_ = os.Remove(fullPath)
		return "", 0, fmt.Errorf("write original: %w", err)
	}
	return filepath.Join(galleryID.String(), "originals", filename), n, nil
}

// GenerateThumbnail creates a thumbnail from image data.
// For 360 photos (is360=true), it crops the center horizontal strip and produces a 2:1 thumb.
// Returns (relThumbPath, origWidth, origHeight, error).
func (p *Processor) GenerateThumbnail(galleryID, photoID uuid.UUID, data []byte, is360 bool) (string, int, int, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, fmt.Errorf("decode image: %w", err)
	}

	bounds := img.Bounds()
	origW := bounds.Dx()
	origH := bounds.Dy()

	var thumb image.Image

	if is360 {
		// Center-fill to 2:1 — maintains correct proportions without distortion.
		thumb = imaging.Fill(img, 800, 400, imaging.Center, imaging.Lanczos)
	} else {
		// Center-fill crop at 4:3, resize to 600×450
		thumb = imaging.Fill(img, 600, 450, imaging.Center, imaging.Lanczos)
	}

	dir := filepath.Join(p.uploadsPath, galleryID.String(), "thumbs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", 0, 0, fmt.Errorf("create thumbs dir: %w", err)
	}
	filename := photoID.String() + "_thumb.jpg"
	thumbFullPath := filepath.Join(dir, filename)
	if err := imaging.Save(thumb, thumbFullPath, imaging.JPEGQuality(85)); err != nil {
		return "", 0, 0, fmt.Errorf("save thumbnail: %w", err)
	}
	_ = os.Chmod(thumbFullPath, 0600)

	relPath := filepath.Join(galleryID.String(), "thumbs", filename)
	return relPath, origW, origH, nil
}

// GenerateVideoThumbnail extracts a frame from a video file using ffmpeg and
// saves it as a JPEG thumbnail.  Falls back to a dark placeholder image if
// ffmpeg is unavailable or fails, so the caller always gets a valid JPEG path.
// The input path must be an absolute filesystem path to the saved original video.
func (p *Processor) GenerateVideoThumbnail(galleryID, photoID uuid.UUID, inputPath string, is360 bool) (string, error) {
	dir := filepath.Join(p.uploadsPath, galleryID.String(), "thumbs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create thumbs dir: %w", err)
	}

	filename := photoID.String() + "_thumb.jpg"
	thumbFullPath := filepath.Join(dir, filename)
	relPath := filepath.Join(galleryID.String(), "thumbs", filename)

	// Try to extract frame at 1 second first, then at 0 for very short clips.
	// Capture stderr so failures surface in logs rather than being silent.
	ffmpegOK := false
	for _, seek := range []string{"1", "0"} {
		var stderr bytes.Buffer
		cmd := exec.Command("ffmpeg",
			"-ss", seek,
			"-i", inputPath,
			"-vframes", "1",
			"-q:v", "2",
			"-y",
			thumbFullPath,
		)
		cmd.Stderr = &stderr
		if err := cmd.Run(); err == nil {
			ffmpegOK = true
			break
		}
	}

	if ffmpegOK {
		// Resize the extracted frame using imaging (same logic as photo thumbnails)
		img, err := imaging.Open(thumbFullPath)
		if err == nil {
			var thumb image.Image
			if is360 {
				thumb = imaging.Fill(img, 800, 400, imaging.Center, imaging.Lanczos)
			} else {
				thumb = imaging.Fill(img, 600, 450, imaging.Center, imaging.Lanczos)
			}
			if err := imaging.Save(thumb, thumbFullPath, imaging.JPEGQuality(85)); err == nil {
				_ = os.Chmod(thumbFullPath, 0600)
				return relPath, nil
			}
		}
	}

	// ffmpeg unavailable or failed — generate a dark placeholder JPEG so that
	// the gallery card renders something instead of a broken-image icon.
	return p.generatePlaceholderThumbnail(thumbFullPath, relPath, is360)
}

// generatePlaceholderThumbnail writes a solid dark-colour JPEG as a fallback
// thumbnail for videos whose frames could not be extracted.
func (p *Processor) generatePlaceholderThumbnail(fullPath, relPath string, is360 bool) (string, error) {
	w, h := 600, 450
	if is360 {
		w, h = 800, 400
	}
	img := imaging.New(w, h, color.NRGBA{R: 18, G: 18, B: 18, A: 255})
	if err := imaging.Save(img, fullPath, imaging.JPEGQuality(60)); err != nil {
		return "", fmt.Errorf("save placeholder thumbnail: %w", err)
	}
	_ = os.Chmod(fullPath, 0600)
	return relPath, nil
}

// DeletePhoto removes original and thumbnail files for a photo from disk.
func (p *Processor) DeletePhoto(storagePath, thumbPath string) {
	if err := os.Remove(filepath.Join(p.uploadsPath, storagePath)); err != nil && !os.IsNotExist(err) {
		slog.Warn("delete original failed", "path", storagePath, "error", err)
	}
	if err := os.Remove(filepath.Join(p.uploadsPath, thumbPath)); err != nil && !os.IsNotExist(err) {
		slog.Warn("delete thumb failed", "path", thumbPath, "error", err)
	}
}

// ServeOriginalPath returns the filesystem path for an original file.
func (p *Processor) ServeOriginalPath(relPath string) string {
	return filepath.Join(p.uploadsPath, relPath)
}

// ServeThumbPath returns the filesystem path for a thumbnail file.
func (p *Processor) ServeThumbPath(relPath string) string {
	return filepath.Join(p.uploadsPath, relPath)
}
