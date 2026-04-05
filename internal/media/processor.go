package media

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
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
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return "", fmt.Errorf("write original: %w", err)
	}
	return filepath.Join(galleryID.String(), "originals", filename), nil
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
		// imaging.Fill scales to cover the target, then crops from centre, so a
		// 2:1 panorama simply resizes to 800×400 and wider panoramas lose only
		// the poles. The result is undistorted content for the CSS pan animation.
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

	relPath := filepath.Join(galleryID.String(), "thumbs", filename)
	return relPath, origW, origH, nil
}

// DeletePhoto removes original and thumbnail files for a photo from disk.
func (p *Processor) DeletePhoto(storagePath, thumbPath string) {
	_ = os.Remove(filepath.Join(p.uploadsPath, storagePath))
	_ = os.Remove(filepath.Join(p.uploadsPath, thumbPath))
}

// ServeOriginalPath returns the filesystem path for an original file.
func (p *Processor) ServeOriginalPath(relPath string) string {
	return filepath.Join(p.uploadsPath, relPath)
}

// ServeThumbPath returns the filesystem path for a thumbnail file.
func (p *Processor) ServeThumbPath(relPath string) string {
	return filepath.Join(p.uploadsPath, relPath)
}
