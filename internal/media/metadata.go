package media

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"
)

type ImageMeta struct {
	MimeType       string
	Width          int
	Height         int
	Is360          bool
	ProjectionType string
	CapturedAt     *time.Time
	ExifData       map[string]any
}

// DetectMIME reads the first 512 bytes to detect MIME type.
// Returns the MIME type and a new reader that starts from the beginning.
func DetectMIME(r io.Reader) (string, io.Reader, error) {
	buf := make([]byte, 512)
	n, err := io.ReadFull(r, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", nil, err
	}
	mime := http.DetectContentType(buf[:n])
	combined := io.MultiReader(bytes.NewReader(buf[:n]), r)
	return mime, combined, nil
}

// IsAllowedMIME checks if the MIME type is allowed for upload.
func IsAllowedMIME(mime string) bool {
	allowed := map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
		"image/webp": true,
		"image/heic": true,
		"image/heif": true,
	}
	return allowed[mime]
}

// ExtractMetadata parses image data and returns metadata.
// Uses lightweight string search for XMP — avoids heavy EXIF library startup cost.
func ExtractMetadata(data []byte, mime string) *ImageMeta {
	meta := &ImageMeta{
		MimeType: mime,
		ExifData: make(map[string]any),
	}

	if mime == "image/jpeg" {
		extractJPEGMeta(data, meta)
	}

	return meta
}

func extractJPEGMeta(data []byte, meta *ImageMeta) {
	// Scan JPEG APP1 segments for XMP data.
	// XMP APP1 marker: FF E1, followed by length, then XMP namespace header.
	xmpHeader := []byte("http://ns.adobe.com/xap/1.0/\x00")

	i := 0
	for i < len(data)-4 {
		// Look for FF marker
		if data[i] != 0xFF {
			i++
			continue
		}
		marker := data[i+1]
		if marker == 0xE1 { // APP1
			if i+4 >= len(data) {
				break
			}
			segLen := int(data[i+2])<<8 | int(data[i+3])
			segEnd := i + 2 + segLen
			if segEnd > len(data) {
				segEnd = len(data)
			}
			segData := data[i+4 : segEnd]

			// Check for XMP namespace header
			if len(segData) > len(xmpHeader) && bytes.Equal(segData[:len(xmpHeader)], xmpHeader) {
				xmpStr := string(segData[len(xmpHeader):])
				if detectIs360FromXMP(xmpStr) {
					meta.Is360 = true
					meta.ProjectionType = "equirectangular"
				}
			}
			i = segEnd
		} else if marker == 0xD8 || marker == 0xD9 {
			// SOI or EOI
			i += 2
		} else if marker == 0xD0 || marker == 0xD7 {
			// RST markers
			i += 2
		} else {
			if i+4 > len(data) {
				break
			}
			segLen := int(data[i+2])<<8 | int(data[i+3])
			i += 2 + segLen
		}
	}
}

func detectIs360FromXMP(xmp string) bool {
	lower := strings.ToLower(xmp)
	// Google Photo Sphere XMP tags
	if strings.Contains(lower, "projectiontype") && strings.Contains(lower, "equirectangular") {
		return true
	}
	if strings.Contains(lower, "usepanoramaviewer") && strings.Contains(lower, "true") {
		return true
	}
	// Some cameras embed this directly
	if strings.Contains(lower, "equirectangular") {
		return true
	}
	// Insta360 specific marker
	if strings.Contains(lower, "insta360") {
		return true
	}
	return false
}

// Detect360FromAspectRatio falls back to aspect ratio check (2:1 ± 15%).
func Detect360FromAspectRatio(width, height int) bool {
	if height == 0 {
		return false
	}
	ratio := float64(width) / float64(height)
	return ratio >= 1.7 && ratio <= 2.3
}
